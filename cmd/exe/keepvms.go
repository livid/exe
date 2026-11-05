package main

// adoptsVMsCommand is the hidden command a stopping daemon asks the binary
// on disk: one that answers adopts the VMs left running (keepvms_linux.go).
// A build from before this prints its usage and exits 2.
const adoptsVMsCommand = "adopts-vms"
