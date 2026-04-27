//go:build ignore

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"ariga.io/atlas/sql/sqltool"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalln("Usage: go run update_hash.go <migrations_dir>")
	}
	migrationsDir := os.Args[1]
	fmt.Printf("Recalculating checksums for directory: %s\n", migrationsDir)

	dir, err := sqltool.NewGolangMigrateDir(migrationsDir)
	if err != nil {
		log.Fatalf("failed creating migration directory: %v", err)
	}

	hashFile, err := dir.Checksum()
	if err != nil {
		log.Fatalf("failed to recalculate checksum: %v", err)
	}

	hashFileBytes, err := hashFile.MarshalText()
	if err != nil {
		log.Fatalf("failed to marshal hash file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationsDir, "atlas.sum"), hashFileBytes, 0644); err != nil {
		log.Fatalf("failed to write atlas.sum file: %v", err)
	}
}

