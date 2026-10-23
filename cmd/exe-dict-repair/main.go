// exe-dict-repair audits English part-of-speech labels in a Dict database.
// By default it only reads. Applying requires a new backup and audit report.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"exe/internal/dictlabels"
	_ "modernc.org/sqlite"
)

type repair struct {
	Source  string              `json:"source"`
	Target  string              `json:"target"`
	Word    string              `json:"word"`
	Changes []dictlabels.Change `json:"changes"`
	Before  string              `json:"before"` // exact original JSON for compare-and-swap and rollback
	After   string              `json:"after"`
}

type problem struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Word   string `json:"word"`
	Error  string `json:"error"`
}

type report struct {
	Time       string    `json:"time"`
	Database   string    `json:"database"`
	Entries    int       `json:"entries"`
	English    int       `json:"english_entries"`
	Repairs    []repair  `json:"repairs"`
	Unresolved []problem `json:"unresolved"`
	Backup     string    `json:"backup,omitempty"`
	Applied    bool      `json:"applied"`
}

func openDatabase(path, mode string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := url.Values{"mode": {mode}, "_pragma": {"busy_timeout(5000)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

func scan(db *sql.DB) (*report, error) {
	r := &report{Time: time.Now().UTC().Format(time.RFC3339), Repairs: []repair{}, Unresolved: []problem{}}
	rows, err := db.Query(`SELECT src, dst, key, entry FROM entries ORDER BY src, dst, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row repair
		if err := rows.Scan(&row.Source, &row.Target, &row.Word, &row.Before); err != nil {
			return nil, err
		}
		r.Entries++
		if row.Source != "en" {
			continue
		}
		r.English++
		after, changes, err := dictlabels.Repair([]byte(row.Before))
		if err != nil {
			r.Unresolved = append(r.Unresolved, problem{row.Source, row.Target, row.Word, err.Error()})
			continue
		}
		if len(changes) > 0 {
			row.After, row.Changes = string(after), changes
			r.Repairs = append(r.Repairs, row)
		}
	}
	return r, rows.Err()
}

func backup(db *sql.DB, path string) error {
	// VACUUM INTO makes a consistent snapshot including committed WAL data.
	// Reserve an empty file first so the backup always has private permissions.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if _, err = db.Exec(`VACUUM INTO ?`, path); err != nil {
		return err
	}
	copy, err := openDatabase(path, "ro")
	if err != nil {
		return err
	}
	defer copy.Close()
	var check string
	if err = copy.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("backup integrity check: %s", check)
	}
	f, err = os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func apply(db *sql.DB, r *report) error {
	if len(r.Unresolved) != 0 {
		return fmt.Errorf("%d entries need review; no entries changed", len(r.Unresolved))
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, row := range r.Repairs {
		result, err := tx.Exec(`UPDATE entries SET entry = ? WHERE src = ? AND dst = ? AND key = ? AND entry = ?`,
			row.After, row.Source, row.Target, row.Word, row.Before)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%s/%s/%s changed since the scan; all repairs rolled back", row.Source, row.Target, row.Word)
		}
	}
	return tx.Commit()
}

func writeReport(f *os.File, r *report) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return err
	}
	return f.Sync()
}

func run() error {
	database := flag.String("db", "", "Dict database path (required; never created)")
	doApply := flag.Bool("apply", false, "apply the audited label changes")
	backupPath := flag.String("backup", "", "new SQLite backup path (required with -apply)")
	reportPath := flag.String("report", "", "new private JSON audit report path (required with -apply)")
	flag.Parse()
	if *database == "" || flag.NArg() != 0 || (*doApply && (*backupPath == "" || *reportPath == "")) {
		flag.Usage()
		return fmt.Errorf("supply -db; applying also requires -backup and -report")
	}
	mode := "ro"
	if *doApply {
		mode = "rw"
	}
	db, err := openDatabase(*database, mode)
	if err != nil {
		return err
	}
	defer db.Close()
	r, err := scan(db)
	if err != nil {
		return err
	}
	r.Database, err = filepath.Abs(*database)
	if err != nil {
		return err
	}
	var reportFile *os.File
	if *reportPath != "" {
		reportFile, err = os.OpenFile(*reportPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer reportFile.Close()
		if err = writeReport(reportFile, r); err != nil {
			return err
		}
	}
	fmt.Printf("Scanned %d entries (%d English): %d entries to repair, %d requiring review.\n",
		r.Entries, r.English, len(r.Repairs), len(r.Unresolved))
	for _, p := range r.Unresolved {
		fmt.Printf("Review %s/%s/%s: %s\n", p.Source, p.Target, p.Word, p.Error)
	}
	if !*doApply {
		return nil
	}
	if len(r.Unresolved) != 0 {
		return fmt.Errorf("unrecognized labels need review before applying")
	}
	if err = backup(db, *backupPath); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	r.Backup = *backupPath
	if err = writeReport(reportFile, r); err != nil {
		return err
	}
	if err = apply(db, r); err != nil {
		return err
	}
	r.Applied = true
	if err = writeReport(reportFile, r); err != nil {
		return fmt.Errorf("repairs committed but report update failed: %w", err)
	}
	fmt.Printf("Repaired %d entries. Backup: %s\n", len(r.Repairs), r.Backup)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
