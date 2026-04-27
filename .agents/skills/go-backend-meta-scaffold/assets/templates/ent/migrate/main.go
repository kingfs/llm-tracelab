//go:build ignore

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"__MODULE_PATH__/ent/dao/migrate"

	"ariga.io/atlas/sql/sqltool"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"

	_ "github.com/lib/pq"
)

const (
	postgresImage = "postgres:17-alpine"
	dbUser        = "postgres"
	dbPass        = "pass"
	dbName        = "test"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		log.Printf("migration generation failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: go run -mod=mod ent/migrate/main.go <migration_name>")
	}
	migrationName := os.Args[1]

	containerID, port, err := startPostgresContainer(ctx)
	if err != nil {
		return fmt.Errorf("start postgres container: %w", err)
	}
	defer func() {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
	}()

	dsn := fmt.Sprintf("postgres://%s:%s@localhost:%s/%s?sslmode=disable", dbUser, dbPass, port, dbName)
	if err := waitForDB(ctx, dsn); err != nil {
		return fmt.Errorf("wait for postgres: %w", err)
	}

	dir, err := sqltool.NewGolangMigrateDir("ent/migrations")
	if err != nil {
		return fmt.Errorf("create migrations dir: %w", err)
	}

	opts := []schema.MigrateOption{
		schema.WithDir(dir),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(dialect.Postgres),
	}

	if err := migrate.NamedDiff(ctx, dsn, migrationName, opts...); err != nil {
		return fmt.Errorf("generate migration diff: %w", err)
	}

	return nil
}

func startPostgresContainer(ctx context.Context) (string, string, error) {
	cmd := exec.CommandContext(
		ctx,
		"docker",
		"run",
		"-P",
		"-d",
		"--rm",
		"-e", fmt.Sprintf("POSTGRES_PASSWORD=%s", dbPass),
		"-e", fmt.Sprintf("POSTGRES_DB=%s", dbName),
		postgresImage,
	)

	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("docker run: %w", err)
	}
	containerID := strings.TrimSpace(string(out))

	time.Sleep(time.Second)

	out, err = exec.CommandContext(ctx, "docker", "port", containerID, "5432/tcp").Output()
	if err != nil {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
		return "", "", fmt.Errorf("docker port: %w", err)
	}

	addr := strings.TrimSpace(string(out))
	_, port, err := net.SplitHostPort(addr)
	if err == nil {
		return containerID, port, nil
	}

	lines := strings.Split(addr, "\n")
	if len(lines) == 0 {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
		return "", "", fmt.Errorf("parse docker port output: %s", addr)
	}

	_, port, err = net.SplitHostPort(lines[0])
	if err != nil {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
		return "", "", fmt.Errorf("parse docker port output %q: %w", addr, err)
	}

	return containerID, port, nil
}

func waitForDB(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("sql open: %w", err)
	}
	defer db.Close()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for db connectivity")
		case <-ticker.C:
			if err := db.PingContext(timeoutCtx); err == nil {
				return nil
			}
		}
	}
}
