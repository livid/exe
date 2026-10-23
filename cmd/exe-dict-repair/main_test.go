package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dict.db")
	db, err := openDatabase(path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`PRAGMA journal_mode=WAL;
	CREATE TABLE entries (src TEXT, dst TEXT, key TEXT, entry TEXT, created INTEGER, looked INTEGER, lookups INTEGER, model TEXT, PRIMARY KEY (src,dst,key));
	CREATE TABLE typos (src TEXT, key TEXT, word TEXT);
	INSERT INTO entries VALUES ('en','zh','empire','{"senses":[{"pos":"名","gloss":"帝国"}],"extra":123}',1,2,3,'original');
	INSERT INTO entries VALUES ('en','zh','emission','{"senses":[{"pos":"n.","gloss":"排放"}]}',4,5,6,'original');
	INSERT INTO entries VALUES ('en','ja','example','{"senses":[{"pos":"名詞","gloss":"例"}]}',7,8,9,'original');
	INSERT INTO entries VALUES ('es','zh','matador','{"senses":[{"pos":"阳、阴","gloss":"斗牛士"}]}',10,11,12,'original');
	INSERT INTO typos VALUES ('en','emprie','empire');`)
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestRepairBackupPreservationAndIdempotence(t *testing.T) {
	db, path := fixture(t)
	r, err := scan(db)
	if err != nil || r.Entries != 4 || r.English != 3 || len(r.Repairs) != 2 || len(r.Unresolved) != 0 {
		t.Fatalf("scan = %+v, %v", r, err)
	}
	copyPath := filepath.Join(filepath.Dir(path), "before.db")
	if err := backup(db, copyPath); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(copyPath)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private")
	}
	if err := backup(db, copyPath); err == nil {
		t.Fatal("overwrote the previous backup")
	}
	if err := apply(db, r); err != nil {
		t.Fatal(err)
	}
	copy, err := openDatabase(copyPath, "ro")
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	original, err := scan(copy)
	if err != nil || len(original.Repairs) != 2 {
		t.Fatal("backup did not retain the original WAL data")
	}
	for _, key := range []string{"empire", "emission", "example", "matador"} {
		var before, after string
		query := `SELECT json_array(src,dst,key,created,looked,lookups,model) FROM entries WHERE key=?`
		if err := copy.QueryRow(query, key).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(query, key).Scan(&after); err != nil || before != after {
			t.Fatalf("metadata changed for %s", key)
		}
		if key == "emission" || key == "matador" {
			copy.QueryRow(`SELECT entry FROM entries WHERE key=?`, key).Scan(&before)
			db.QueryRow(`SELECT entry FROM entries WHERE key=?`, key).Scan(&after)
			if before != after {
				t.Fatalf("changed correct/non-English entry %s", key)
			}
		}
	}
	var typo string
	if err := db.QueryRow(`SELECT word FROM typos WHERE key='emprie'`).Scan(&typo); err != nil || typo != "empire" {
		t.Fatal("typo mapping changed")
	}
	again, err := scan(db)
	if err != nil || len(again.Repairs) != 0 || len(again.Unresolved) != 0 {
		t.Fatalf("second scan = %+v, %v", again, err)
	}
}

func TestConcurrentChangeRollsBackEntireRepair(t *testing.T) {
	db, _ := fixture(t)
	r, err := scan(db)
	if err != nil {
		t.Fatal(err)
	}
	last := r.Repairs[len(r.Repairs)-1]
	if _, err := db.Exec(`UPDATE entries SET entry='{"senses":[{"pos":"v."}]}' WHERE src=? AND dst=? AND key=?`, last.Source, last.Target, last.Word); err != nil {
		t.Fatal(err)
	}
	if err := apply(db, r); err == nil {
		t.Fatal("overwrote a concurrent update")
	}
	var actual string
	first := r.Repairs[0]
	db.QueryRow(`SELECT entry FROM entries WHERE src=? AND dst=? AND key=?`, first.Source, first.Target, first.Word).Scan(&actual)
	if actual != r.Repairs[0].Before {
		t.Fatal("earlier update was not rolled back")
	}
}

func TestUnrecognizedLabelsPreventWrites(t *testing.T) {
	db, _ := fixture(t)
	db.Exec(`UPDATE entries SET entry='{"senses":[{"pos":"名（未知）"}]}' WHERE key='example'`)
	r, err := scan(db)
	if err != nil || len(r.Unresolved) != 1 {
		t.Fatalf("scan = %+v, %v", r, err)
	}
	if err := apply(db, r); err == nil {
		t.Fatal("applied a repair with unresolved labels")
	}
}
