package agent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// VulnerabilityState represents the lifecycle state of a vulnerability.
type VulnerabilityState string

const (
	StateOpen  VulnerabilityState = "OPEN"
	StateFixed VulnerabilityState = "FIXED"
)

// VulnerabilityRecord represents a vulnerability in the database.
type VulnerabilityRecord struct {
	ID              string // hash(cve + workload + package + container)
	CVE             string
	Workload        string // namespace/kind/name
	Severity        string
	Image           string // package:version (legacy, kept for compatibility)
	ContainerName   string
	ImageRepository string
	ImageTag        string
	ImageDigest     string
	FirstSeenDigest string // digest when vulnerability was first discovered (immutable)
	FixedVersion    string // version that fixes this vulnerability (from Trivy)
	State           VulnerabilityState
	FirstSeen       time.Time
	LastSeen        time.Time
	FixedAt         *time.Time
}

// DB wraps the SQLite connection and provides vulnerability operations.
type DB struct {
	conn *sql.DB
}

// NewDB creates a new database connection and ensures schema exists.
func NewDB(ctx context.Context, dbPath string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := conn.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// SQLite optimizations
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=10000",
		"PRAGMA temp_store=MEMORY",
	}
	for _, pragma := range pragmas {
		// Pragmas are optimizations, not critical - ignore errors
		_, _ = conn.ExecContext(ctx, pragma)
	}

	db := &DB{conn: conn}

	// Ensure schema exists
	if err := db.migrate(ctx); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// migrate ensures the database schema exists.
func (db *DB) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS vulnerabilities (
		id TEXT PRIMARY KEY,
		cve TEXT NOT NULL,
		workload TEXT NOT NULL,
		severity TEXT NOT NULL,
		image TEXT,
		container_name TEXT,
		image_repository TEXT,
		image_tag TEXT,
		image_digest TEXT,
		first_seen_digest TEXT,
		fixed_version TEXT,
		state TEXT NOT NULL DEFAULT 'OPEN',
		first_seen TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		fixed_at TEXT,
		saas_synced INTEGER NOT NULL DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_vuln_state ON vulnerabilities(state);
	CREATE INDEX IF NOT EXISTS idx_vuln_severity ON vulnerabilities(severity);
	CREATE INDEX IF NOT EXISTS idx_vuln_cve ON vulnerabilities(cve);
	CREATE INDEX IF NOT EXISTS idx_vuln_workload ON vulnerabilities(workload);
	CREATE INDEX IF NOT EXISTS idx_vuln_saas_synced ON vulnerabilities(saas_synced) WHERE saas_synced = 0;
	CREATE INDEX IF NOT EXISTS idx_vuln_image_digest ON vulnerabilities(image_digest) WHERE image_digest IS NOT NULL;
	`

	if _, err := db.conn.ExecContext(ctx, schema); err != nil {
		return err
	}

	// Migrations for existing databases (SQLite doesn't have ADD COLUMN IF NOT EXISTS)
	_, _ = db.conn.ExecContext(ctx, "ALTER TABLE vulnerabilities ADD COLUMN fixed_version TEXT")
	_, _ = db.conn.ExecContext(ctx, "ALTER TABLE vulnerabilities ADD COLUMN first_seen_digest TEXT")

	// Backfill first_seen_digest from image_digest for existing records
	_, _ = db.conn.ExecContext(ctx, "UPDATE vulnerabilities SET first_seen_digest = image_digest WHERE first_seen_digest IS NULL AND image_digest IS NOT NULL")

	return nil
}

// MarkSaasSynced marks vulnerabilities as synced to SaaS.
func (db *DB) MarkSaasSynced(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, "UPDATE vulnerabilities SET saas_synced = 1 WHERE id = ?")
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetUnsyncedVulnerabilities returns vulnerabilities that haven't been synced to SaaS.
func (db *DB) GetUnsyncedVulnerabilities(ctx context.Context) ([]VulnerabilityRecord, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, cve, workload, severity, COALESCE(image, ''),
		       COALESCE(container_name, ''), COALESCE(image_repository, ''), COALESCE(image_tag, ''), COALESCE(image_digest, ''),
		       COALESCE(first_seen_digest, ''), COALESCE(fixed_version, ''), state, first_seen, last_seen, fixed_at
		FROM vulnerabilities
		WHERE saas_synced = 0
		ORDER BY first_seen ASC
		LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var vulns []VulnerabilityRecord
	for rows.Next() {
		var v VulnerabilityRecord
		var firstSeen, lastSeen string
		var fixedAt sql.NullString

		if err := rows.Scan(&v.ID, &v.CVE, &v.Workload, &v.Severity, &v.Image,
			&v.ContainerName, &v.ImageRepository, &v.ImageTag, &v.ImageDigest,
			&v.FirstSeenDigest, &v.FixedVersion, &v.State, &firstSeen, &lastSeen, &fixedAt); err != nil {
			return nil, err
		}

		v.FirstSeen, _ = time.Parse(time.RFC3339, firstSeen)
		v.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
		if fixedAt.Valid {
			t, _ := time.Parse(time.RFC3339, fixedAt.String)
			v.FixedAt = &t
		}

		vulns = append(vulns, v)
	}

	return vulns, rows.Err()
}

// UpsertVulnerability inserts or updates a vulnerability record.
// Returns true if this is a new vulnerability.
func (db *DB) UpsertVulnerability(ctx context.Context, v *VulnerabilityRecord) (isNew bool, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Check if exists
	var existingState string
	err = db.conn.QueryRowContext(ctx,
		"SELECT state FROM vulnerabilities WHERE id = ?",
		v.ID,
	).Scan(&existingState)

	if err == sql.ErrNoRows {
		// New vulnerability - insert with first_seen_digest (immutable)
		_, err = db.conn.ExecContext(ctx, `
			INSERT INTO vulnerabilities (id, cve, workload, severity, image, container_name, image_repository, image_tag, image_digest, first_seen_digest, fixed_version, state, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, v.ID, v.CVE, v.Workload, v.Severity, v.Image, v.ContainerName, v.ImageRepository, v.ImageTag, v.ImageDigest, v.ImageDigest, v.FixedVersion, StateOpen, now, now)
		return true, err
	}

	if err != nil {
		return false, err
	}

	// Existing vulnerability - update last_seen, reopen if was fixed
	if existingState == string(StateFixed) {
		// Reopened! Reset saas_synced so reopen event gets sent to SaaS
		_, err = db.conn.ExecContext(ctx, `
			UPDATE vulnerabilities
			SET state = ?, last_seen = ?, fixed_at = NULL, severity = ?, image = ?,
			    container_name = ?, image_repository = ?, image_tag = ?, image_digest = ?,
			    fixed_version = ?, saas_synced = 0
			WHERE id = ?
		`, StateOpen, now, v.Severity, v.Image, v.ContainerName, v.ImageRepository, v.ImageTag, v.ImageDigest, v.FixedVersion, v.ID)
		return true, err // Treat reopen as "new" for notification purposes
	}

	// Just update last_seen and image info
	_, err = db.conn.ExecContext(ctx, `
		UPDATE vulnerabilities
		SET last_seen = ?, severity = ?, image = ?, container_name = ?, image_repository = ?, image_tag = ?, image_digest = ?, fixed_version = ?
		WHERE id = ?
	`, now, v.Severity, v.Image, v.ContainerName, v.ImageRepository, v.ImageTag, v.ImageDigest, v.FixedVersion, v.ID)
	return false, err
}

// DigestKey uniquely identifies a workload+container combination for digest tracking.
type DigestKey struct {
	Workload      string // namespace/kind/name
	ContainerName string
}

// MarkFixed marks vulnerabilities as fixed if they weren't seen in the current scan
// AND the image digest has changed (indicating the image was actually updated).
//
// Returns the list of vulnerabilities that were marked as fixed.
//
// Logic:
//   - CVE in scan = stays open (handled by UpsertVulnerability)
//   - CVE not in scan + same digest = stays open (scan may have failed, or CVE removed from DB)
//   - CVE not in scan + different digest = fixed (image was updated)
//   - CVE not in scan + no digest info = stays open (can't confirm fix)
//
// IMPORTANT: If currentIDs is empty, we do NOT mark everything as fixed.
// An empty scan could mean Trivy is down, CRDs are missing, or network issues.
func (db *DB) MarkFixed(ctx context.Context, currentIDs []string, currentDigests map[DigestKey]string) ([]VulnerabilityRecord, error) {
	if len(currentIDs) == 0 {
		// No vulnerabilities in current scan - DO NOT assume everything is fixed.
		// This could be due to Trivy being down, missing CRDs, or other issues.
		// It's safer to leave vulnerabilities as open than to falsely mark them fixed.
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Build a set of current IDs for efficient lookup
	currentSet := make(map[string]bool, len(currentIDs))
	for _, id := range currentIDs {
		currentSet[id] = true
	}

	// Get all open vulnerabilities
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, cve, workload, severity, COALESCE(image, ''),
		       COALESCE(container_name, ''), COALESCE(image_repository, ''), COALESCE(image_tag, ''), COALESCE(image_digest, ''),
		       COALESCE(first_seen_digest, ''), COALESCE(fixed_version, ''), first_seen
		FROM vulnerabilities
		WHERE state = ?
	`, StateOpen)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var toFix []VulnerabilityRecord
	for rows.Next() {
		var v VulnerabilityRecord
		var firstSeen string
		if err := rows.Scan(&v.ID, &v.CVE, &v.Workload, &v.Severity, &v.Image,
			&v.ContainerName, &v.ImageRepository, &v.ImageTag, &v.ImageDigest,
			&v.FirstSeenDigest, &v.FixedVersion, &firstSeen); err != nil {
			return nil, err
		}

		// If still in current scan, skip (already handled by UpsertVulnerability)
		if currentSet[v.ID] {
			continue
		}

		// CVE not in current scan - check if digest changed
		key := DigestKey{Workload: v.Workload, ContainerName: v.ContainerName}
		currentDigest, hasDigest := currentDigests[key]

		// Only mark as fixed if:
		// 1. We have digest info for both first_seen and current
		// 2. The digest has actually changed (image was updated)
		if !hasDigest || v.FirstSeenDigest == "" {
			// No digest info available - can't confirm fix, keep open
			// This is safer than falsely marking as fixed
			continue
		}

		if currentDigest == v.FirstSeenDigest {
			// Same digest - image wasn't updated, CVE can't be fixed
			// The CVE might have disappeared from scan for other reasons (DB issue, etc.)
			continue
		}

		// Digest changed - image was updated, mark as fixed
		v.FirstSeen, _ = time.Parse(time.RFC3339, firstSeen)
		v.State = StateFixed
		fixedAt := time.Now()
		v.FixedAt = &fixedAt
		toFix = append(toFix, v)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Update the database
	for _, v := range toFix {
		_, err := db.conn.ExecContext(ctx, `
			UPDATE vulnerabilities
			SET state = ?, fixed_at = ?, saas_synced = 0
			WHERE id = ?
		`, StateFixed, now, v.ID)
		if err != nil {
			return nil, err
		}
	}

	return toFix, nil
}

// GetOpenVulnerabilities returns all open vulnerabilities.
func (db *DB) GetOpenVulnerabilities(ctx context.Context) ([]VulnerabilityRecord, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, cve, workload, severity, COALESCE(image, ''),
		       COALESCE(container_name, ''), COALESCE(image_repository, ''), COALESCE(image_tag, ''), COALESCE(image_digest, ''),
		       COALESCE(first_seen_digest, ''), COALESCE(fixed_version, ''), state, first_seen, last_seen, fixed_at
		FROM vulnerabilities WHERE state = ?
		ORDER BY
			CASE severity
				WHEN 'CRITICAL' THEN 1
				WHEN 'HIGH' THEN 2
				WHEN 'MEDIUM' THEN 3
				WHEN 'LOW' THEN 4
				ELSE 5
			END,
			first_seen DESC
	`, StateOpen)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var vulns []VulnerabilityRecord
	for rows.Next() {
		var v VulnerabilityRecord
		var firstSeen, lastSeen string
		var fixedAt sql.NullString

		if err := rows.Scan(&v.ID, &v.CVE, &v.Workload, &v.Severity, &v.Image,
			&v.ContainerName, &v.ImageRepository, &v.ImageTag, &v.ImageDigest,
			&v.FirstSeenDigest, &v.FixedVersion, &v.State, &firstSeen, &lastSeen, &fixedAt); err != nil {
			return nil, err
		}

		v.FirstSeen, _ = time.Parse(time.RFC3339, firstSeen)
		v.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
		if fixedAt.Valid {
			t, _ := time.Parse(time.RFC3339, fixedAt.String)
			v.FixedAt = &t
		}

		vulns = append(vulns, v)
	}

	return vulns, rows.Err()
}

// Stats returns counts of vulnerabilities by state and severity.
type Stats struct {
	TotalOpen  int
	TotalFixed int
	BySeverity map[string]int
}

// GetStats returns vulnerability statistics.
func (db *DB) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{
		BySeverity: make(map[string]int),
	}

	// Count by state
	err := db.conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM vulnerabilities WHERE state = ?", StateOpen,
	).Scan(&stats.TotalOpen)
	if err != nil {
		return nil, err
	}

	err = db.conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM vulnerabilities WHERE state = ?", StateFixed,
	).Scan(&stats.TotalFixed)
	if err != nil {
		return nil, err
	}

	// Count by severity (open only)
	rows, err := db.conn.QueryContext(ctx, `
		SELECT severity, COUNT(*) FROM vulnerabilities
		WHERE state = ? GROUP BY severity
	`, StateOpen)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var sev string
		var count int
		if err := rows.Scan(&sev, &count); err != nil {
			return nil, err
		}
		stats.BySeverity[sev] = count
	}

	return stats, rows.Err()
}
