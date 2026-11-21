package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type ArtifactData struct {
	Title     string     `json:"title"`
	Mode      string     `json:"mode" jsonschema:"document or deck; each page of a deck is a slide"`
	CatalogID string     `json:"catalog_id"`
	Pages     []ViewPage `json:"pages"`
}

type ArtifactSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Mode      string    `json:"mode"`
	CatalogID string    `json:"catalog_id"`
	Version   int64     `json:"version"`
	AgentID   string    `json:"agent_id,omitempty"`
	Tags      []string  `json:"tags"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Artifact struct {
	ArtifactSummary
	Pages []ViewPage `json:"pages"`
}

type CreateArtifactInput struct {
	Meta
	ArtifactData
	Tags []string `json:"tags,omitempty" jsonschema:"optional topical tags; trimmed, lowercased and deduplicated"`
}

type UpdateArtifactInput struct {
	EditInput
	ArtifactData
	Tags *[]string `json:"tags,omitempty" jsonschema:"replace the tags; omit to keep them or send [] to clear them"`
}

type ListArtifactsInput struct {
	AgentID string `json:"agent_id,omitempty" jsonschema:"only artifacts created by this agent ID; omit for all artifacts"`
	Limit   int    `json:"limit,omitempty" jsonschema:"1-200, default 50"`
	Offset  int    `json:"offset,omitempty" jsonschema:"0-1000000, default 0"`
}

func normalizeArtifactTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if _, exists := seen[tag]; tag == "" || exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized
}

func (d ArtifactData) validate() error {
	if strings.TrimSpace(d.Title) == "" || len(d.Title) > 200 {
		return invalid("Artifact title must be 1-200 bytes")
	}
	if !oneOf(d.Mode, "document", "deck") {
		return invalid("Artifact mode must be document or deck")
	}
	if _, err := trackerCatalog(d.CatalogID); err != nil {
		return err
	}
	if err := validatePages(d.Pages, nil); err != nil {
		return err
	}
	for _, page := range d.Pages {
		for _, section := range page.Sections {
			for _, block := range section.Blocks {
				if block.Kind == "tasks" || block.Kind == "events" {
					return invalid("Tasks and events blocks require a subject-linked view")
				}
			}
		}
	}
	return nil
}

const artifactColumns = `id::text,title,mode,catalog_id,version,COALESCE(agent_id,''),tags,updated_at`

func listArtifacts(ctx context.Context, u *unit, in ListArtifactsInput) (Page[ArtifactSummary], error) {
	result := Page[ArtifactSummary]{Items: []ArtifactSummary{}}
	limit, offset, _, err := paging(ListInput{Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return result, err
	}
	where := ""
	var args []any
	if in.AgentID != "" {
		where = " WHERE agent_id=$1"
		args = append(args, in.AgentID)
	}
	if err = u.tx.QueryRow(ctx, `SELECT count(*) FROM life.artifacts`+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	// Put the paging parameters first in the query arguments regardless of filtering.
	where = ""
	args = []any{limit, offset}
	if in.AgentID != "" {
		where = " WHERE agent_id=$3"
		args = append(args, in.AgentID)
	}
	rows, err := u.tx.Query(ctx, `SELECT `+artifactColumns+` FROM life.artifacts`+where+` ORDER BY updated_at DESC,id DESC LIMIT $1 OFFSET $2`, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var a ArtifactSummary
		if err = rows.Scan(&a.ID, &a.Title, &a.Mode, &a.CatalogID, &a.Version, &a.AgentID, &a.Tags, &a.UpdatedAt); err != nil {
			return result, err
		}
		a.UpdatedAt = a.UpdatedAt.UTC()
		result.Items = append(result.Items, a)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}

func getArtifact(ctx context.Context, u *unit, in GetInput) (Artifact, error) {
	var a Artifact
	key, err := id(in.ID)
	if err != nil {
		return a, err
	}
	var pages []byte
	err = u.tx.QueryRow(ctx, `SELECT `+artifactColumns+`,pages FROM life.artifacts WHERE id=$1`, key).
		Scan(&a.ID, &a.Title, &a.Mode, &a.CatalogID, &a.Version, &a.AgentID, &a.Tags, &a.UpdatedAt, &pages)
	if err != nil {
		return a, err
	}
	a.UpdatedAt = a.UpdatedAt.UTC()
	err = json.Unmarshal(pages, &a.Pages)
	return a, err
}

func createArtifact(ctx context.Context, u *unit, in CreateArtifactInput) (Artifact, error) {
	if err := in.ArtifactData.validate(); err != nil {
		return Artifact{}, err
	}
	key, err := createID("")
	if err != nil {
		return Artifact{}, err
	}
	pages, err := json.Marshal(in.Pages)
	if err != nil {
		return Artifact{}, err
	}
	a := Artifact{
		ArtifactSummary: ArtifactSummary{
			ID: key, Title: in.Title, Mode: in.Mode, CatalogID: in.CatalogID,
			AgentID: artifactCreator(ctx), Tags: normalizeArtifactTags(in.Tags),
		},
		Pages: in.Pages,
	}
	err = u.tx.QueryRow(ctx, `INSERT INTO life.artifacts(id,title,mode,catalog_id,pages,agent_id,tags)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7) RETURNING version,updated_at`,
		key, a.Title, a.Mode, a.CatalogID, pages, a.AgentID, a.Tags).Scan(&a.Version, &a.UpdatedAt)
	if err != nil {
		return Artifact{}, err
	}
	a.UpdatedAt = a.UpdatedAt.UTC()
	return a, u.audit(ctx, "artifact", a.ID, nil, a)
}

func updateArtifact(ctx context.Context, u *unit, in UpdateArtifactInput) (Artifact, error) {
	if err := in.ArtifactData.validate(); err != nil {
		return Artifact{}, err
	}
	old, err := getArtifact(ctx, u, GetInput{ID: in.ID})
	if err != nil {
		return Artifact{}, err
	}
	if err = version(in.ExpectedVersion, old.Version); err != nil {
		return Artifact{}, err
	}
	pages, err := json.Marshal(in.Pages)
	if err != nil {
		return Artifact{}, err
	}
	a := old
	a.Title, a.Mode, a.CatalogID, a.Pages = in.Title, in.Mode, in.CatalogID, in.Pages
	if in.Tags != nil {
		a.Tags = normalizeArtifactTags(*in.Tags)
	}
	err = u.tx.QueryRow(ctx, `UPDATE life.artifacts
		SET title=$2,mode=$3,catalog_id=$4,pages=$5,tags=$6,version=version+1,updated_at=now()
		WHERE id=$1 RETURNING version,updated_at`,
		a.ID, a.Title, a.Mode, a.CatalogID, pages, a.Tags).Scan(&a.Version, &a.UpdatedAt)
	if err != nil {
		return Artifact{}, err
	}
	a.UpdatedAt = a.UpdatedAt.UTC()
	return a, u.audit(ctx, "artifact", a.ID, old, a)
}
