package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// A Library item is either a standalone artifact or a view attached to a subject. One index lists both so a client does
// not have to know which kind an id is before asking for it.
type LibraryItem struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind" jsonschema:"artifact or view"`
	Title       string    `json:"title"`
	Mode        string    `json:"mode" jsonschema:"document, deck or view"`
	CatalogID   string    `json:"catalog_id"`
	Version     int64     `json:"version"`
	AgentID     string    `json:"agent_id,omitempty"`
	Tags        []string  `json:"tags"`
	SubjectKind string    `json:"subject_kind,omitempty" jsonschema:"for a view: intention, task or event"`
	SubjectID   string    `json:"subject_id,omitempty" jsonschema:"for a view: the subject's UUID"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type LibraryDetail struct {
	LibraryItem
	Pages []ViewPage `json:"pages"`
}
type ListLibraryInput struct {
	AgentID string `json:"agent_id,omitempty" jsonschema:"only items created by this agent ID"`
	Kind    string `json:"kind,omitempty" jsonschema:"artifact, view, or omitted for both"`
	Limit   int    `json:"limit,omitempty" jsonschema:"1-200, default 50"`
	Offset  int    `json:"offset,omitempty" jsonschema:"0-1000000, default 0"`
}

const libraryItems = `
WITH items AS (
  SELECT id::text AS id, 'artifact'::text AS kind, title, split_part(template, '.', 1) AS mode, ''::text AS catalog_id, version,
         COALESCE(agent_id, '') AS agent_id, tags, ''::text AS subject_kind, ''::text AS subject_id, updated_at
  FROM life.artifacts
  WHERE deleted_at IS NULL
  UNION ALL
  SELECT v.id::text, 'view'::text,
         COALESCE(CASE v.subject_kind
           WHEN 'intention' THEN (SELECT title FROM life.intentions WHERE id = v.subject_id)
           WHEN 'task' THEN (SELECT title FROM life.tasks WHERE id = v.subject_id)
           WHEN 'event' THEN (SELECT title FROM life.events WHERE id = v.subject_id)
         END, 'View'),
         'view'::text, v.catalog_id, v.version, COALESCE(v.agent_id, ''), v.tags,
         v.subject_kind, v.subject_id::text, v.updated_at
  FROM life.views v
  WHERE v.deleted_at IS NULL
)`

func listLibrary(ctx context.Context, u *unit, in ListLibraryInput) (Page[LibraryItem], error) {
	result := Page[LibraryItem]{Items: []LibraryItem{}}
	limit, offset, _, err := paging(ListInput{Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return result, err
	}
	if in.Kind != "" && in.Kind != "artifact" && in.Kind != "view" {
		return result, invalid("kind must be artifact or view")
	}
	if err = u.tx.QueryRow(ctx, `SELECT count(*) FROM (`+libraryItems+` SELECT * FROM items WHERE ($1='' OR agent_id=$1) AND ($2='' OR kind=$2)) counts`,
		in.AgentID, in.Kind).Scan(&result.Total); err != nil {
		return result, err
	}
	rows, err := u.tx.Query(ctx, `SELECT id,kind,title,mode,catalog_id,version,agent_id,tags,subject_kind,subject_id,updated_at
		FROM (`+libraryItems+` SELECT * FROM items WHERE ($3='' OR agent_id=$3) AND ($4='' OR kind=$4)) listed
		ORDER BY updated_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset, in.AgentID, in.Kind)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item LibraryItem
		if err = rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Mode, &item.CatalogID, &item.Version, &item.AgentID,
			&item.Tags, &item.SubjectKind, &item.SubjectID, &item.UpdatedAt); err != nil {
			return result, err
		}
		item.UpdatedAt = item.UpdatedAt.UTC()
		result.Items = append(result.Items, item)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}

func getLibrary(ctx context.Context, u *unit, in GetInput) (LibraryDetail, error) {
	if a, err := getArtifact(ctx, u, in); err == nil {
		var doc DocParams // a template without pages shows none until the Library goes (ADR 0011)
		_ = json.Unmarshal(a.Params, &doc)
		pages, err := json.Marshal(doc.Pages)
		if err != nil {
			return LibraryDetail{}, err
		}
		var shown []ViewPage
		_ = json.Unmarshal(pages, &shown)
		return LibraryDetail{LibraryItem: LibraryItem{
			ID: a.ID, Kind: "artifact", Title: a.Title, Mode: strings.SplitN(a.Template, ".", 2)[0], Version: a.Version,
			AgentID: a.AgentID, Tags: a.Tags, UpdatedAt: a.UpdatedAt,
		}, Pages: shown}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return LibraryDetail{}, err
	}
	v, err := u.getView(ctx, in.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LibraryDetail{}, notFound()
		}
		return LibraryDetail{}, err
	}
	return LibraryDetail{LibraryItem: LibraryItem{
		ID: v.ID, Kind: "view", Title: subjectTitle(ctx, u, v.SubjectKind, v.SubjectID), Mode: "view",
		CatalogID: v.CatalogID, Version: v.Version, AgentID: v.AgentID, Tags: v.Tags,
		SubjectKind: v.SubjectKind, SubjectID: v.SubjectID, UpdatedAt: v.UpdatedAt,
	}, Pages: v.Pages}, nil
}

func subjectTitle(ctx context.Context, u *unit, kind, subject string) string {
	table := map[string]string{"intention": "intentions", "task": "tasks", "event": "events"}[kind]
	if table == "" {
		return "View"
	}
	var title string
	if err := u.tx.QueryRow(ctx, `SELECT title FROM life.`+table+` WHERE id=$1`, subject).Scan(&title); err != nil || title == "" {
		return "View"
	}
	return title
}
