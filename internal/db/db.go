// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// Package db provides direct pgx/v5 query functions for the venv_build table.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VenvBuild represents a row from the venv_build table.
type VenvBuild struct {
	ID                   int64
	Name                 string
	Version              string
	Description          *string
	Status               string
	CreatorID            *string
	CreatorEmail         *string
	IndexArtifactID      *int64
	IndexArtifactVersion *string
	CIBuildName          *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// CreateParams holds the fields for creating a new VenvBuild row.
type CreateParams struct {
	Name                 string
	Version              string
	Description          *string
	CreatorID            *string
	CreatorEmail         *string
	IndexArtifactID      *int64
	IndexArtifactVersion *string
}

// ListParams holds pagination and filter options for listing VenvBuilds.
type ListParams struct {
	Page      int
	PageSize  int
	Status    string
	Name      string
	CreatorID string
	SortBy    string
	SortDir   string
}

// Queries wraps a pgxpool.Pool and provides typed query methods.
type Queries struct {
	pool *pgxpool.Pool
}

// New creates a new Queries instance backed by pool.
func New(pool *pgxpool.Pool) *Queries {
	return &Queries{pool: pool}
}

const scanCols = ` id, name, version, description, status, creator_id, creator_email,
	index_artifact_id, index_artifact_version, ci_build_name, created_at, updated_at `

func scan(row pgx.Row) (VenvBuild, error) {
	var b VenvBuild
	err := row.Scan(
		&b.ID, &b.Name, &b.Version, &b.Description, &b.Status,
		&b.CreatorID, &b.CreatorEmail,
		&b.IndexArtifactID, &b.IndexArtifactVersion,
		&b.CIBuildName, &b.CreatedAt, &b.UpdatedAt,
	)
	return b, err
}

// CreateVenvBuild inserts a new PENDING build row and returns the generated ID.
func (q *Queries) CreateVenvBuild(ctx context.Context, p CreateParams) (int64, error) {
	const query = `
		INSERT INTO venv_build
			(name, version, description, status, creator_id, creator_email,
			 index_artifact_id, index_artifact_version, created_at, updated_at)
		VALUES ($1,$2,$3,'PENDING',$4,$5,$6,$7,NOW(),NOW())
		RETURNING id`
	var id int64
	err := q.pool.QueryRow(ctx, query,
		p.Name, p.Version, p.Description, p.CreatorID, p.CreatorEmail,
		p.IndexArtifactID, p.IndexArtifactVersion,
	).Scan(&id)
	return id, err
}

// UpdateCIBuildName sets the ci_build_name for the given build ID.
func (q *Queries) UpdateCIBuildName(ctx context.Context, id int64, ciBuildName string) error {
	const query = `UPDATE venv_build SET ci_build_name=$1, updated_at=NOW() WHERE id=$2`
	_, err := q.pool.Exec(ctx, query, ciBuildName, id)
	return err
}

// UpdateStatus sets the status for the given build ID.
func (q *Queries) UpdateStatus(ctx context.Context, id int64, status string) error {
	const query = `UPDATE venv_build SET status=$1, updated_at=NOW() WHERE id=$2`
	_, err := q.pool.Exec(ctx, query, status, id)
	return err
}

// GetVenvBuild retrieves a build by ID. Returns pgx.ErrNoRows if not found.
func (q *Queries) GetVenvBuild(ctx context.Context, id int64) (VenvBuild, error) {
	row := q.pool.QueryRow(ctx, `SELECT`+scanCols+`FROM venv_build WHERE id=$1`, id)
	return scan(row)
}

// GetVenvBuildByNameAndVersion retrieves a build by (name, version) pair.
// Returns pgx.ErrNoRows if not found.
func (q *Queries) GetVenvBuildByNameAndVersion(ctx context.Context, name, version string) (VenvBuild, error) {
	row := q.pool.QueryRow(ctx,
		`SELECT`+scanCols+`FROM venv_build WHERE name=$1 AND version=$2`,
		name, version)
	return scan(row)
}

// CountVenvBuilds returns the total row count matching the list filters.
func (q *Queries) CountVenvBuilds(ctx context.Context, p ListParams) (int64, error) {
	where, args := buildWhere(p)
	row := q.pool.QueryRow(ctx, "SELECT COUNT(*) FROM venv_build WHERE "+where, args...)
	var n int64
	return n, row.Scan(&n)
}

// ListVenvBuilds returns a paginated, filtered, sorted slice of VenvBuilds.
func (q *Queries) ListVenvBuilds(ctx context.Context, p ListParams) ([]VenvBuild, error) {
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}

	sortBy := sanitizeSortBy(p.SortBy)
	sortDir := "DESC"
	if strings.EqualFold(p.SortDir, "asc") {
		sortDir = "ASC"
	}

	where, args := buildWhere(p)
	argN := len(args) + 1
	query := fmt.Sprintf(
		`SELECT`+scanCols+`FROM venv_build WHERE %s ORDER BY %s %s LIMIT $%d OFFSET $%d`,
		where, sortBy, sortDir, argN, argN+1,
	)
	args = append(args, p.PageSize, p.Page*p.PageSize)

	rows, err := q.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []VenvBuild
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, err
		}
		builds = append(builds, b)
	}
	return builds, rows.Err()
}

// buildWhere constructs a WHERE clause and argument list from ListParams.
func buildWhere(p ListParams) (string, []interface{}) {
	var conds []string
	var args []interface{}
	n := 1
	if p.Status != "" {
		conds = append(conds, fmt.Sprintf("status=$%d", n))
		args = append(args, p.Status)
		n++
	}
	if p.Name != "" {
		conds = append(conds, fmt.Sprintf("lower(name) LIKE $%d", n))
		args = append(args, "%"+strings.ToLower(p.Name)+"%")
		n++
	}
	if p.CreatorID != "" {
		conds = append(conds, fmt.Sprintf("creator_id=$%d", n))
		args = append(args, p.CreatorID)
	}
	if len(conds) == 0 {
		return "TRUE", args
	}
	return strings.Join(conds, " AND "), args
}

var allowedSortFields = map[string]string{
	"created_at": "created_at",
	"createdAt":  "created_at",
	"updated_at": "updated_at",
	"updatedAt":  "updated_at",
	"name":       "name",
	"version":    "version",
	"status":     "status",
}

func sanitizeSortBy(s string) string {
	if col, ok := allowedSortFields[s]; ok {
		return col
	}
	return "created_at"
}
