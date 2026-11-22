package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type ArtifactData struct {
	Title    string           `json:"title"`
	Template string           `json:"template" jsonschema:"a template id from templates_get, such as document.v1"`
	Params   json.RawMessage  `json:"params" jsonschema:"the template's params object; its shape is params_schema in templates_get"`
	Sources  []ArtifactSource `json:"sources,omitempty" jsonschema:"where the template's data comes from; omit for a document that needs none"`
}

type ArtifactSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Template  string    `json:"template"`
	Version   int64     `json:"version"`
	AgentID   string    `json:"agent_id,omitempty"`
	Tags      []string  `json:"tags"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Artifact struct {
	ArtifactSummary
	Params  json.RawMessage  `json:"params"`
	Sources []ArtifactSource `json:"sources"`
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

const summaryColumns = `id::text,title,template,version,COALESCE(agent_id,''),tags,updated_at`

func listArtifacts(ctx context.Context, u *unit, in ListArtifactsInput) (Page[ArtifactSummary], error) {
	result := Page[ArtifactSummary]{Items: []ArtifactSummary{}}
	limit, offset, _, err := paging(ListInput{Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return result, err
	}
	where := " WHERE deleted_at IS NULL"
	var args []any
	if in.AgentID != "" {
		where += " AND agent_id=$1"
		args = append(args, in.AgentID)
	}
	if err = u.tx.QueryRow(ctx, `SELECT count(*) FROM life.artifacts`+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	// Put the paging parameters first in the query arguments regardless of filtering.
	where = " WHERE deleted_at IS NULL"
	args = []any{limit, offset}
	if in.AgentID != "" {
		where += " AND agent_id=$3"
		args = append(args, in.AgentID)
	}
	rows, err := u.tx.Query(ctx, `SELECT `+summaryColumns+` FROM life.artifacts`+where+` ORDER BY updated_at DESC,id DESC LIMIT $1 OFFSET $2`, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var a ArtifactSummary
		if err = rows.Scan(&a.ID, &a.Title, &a.Template, &a.Version, &a.AgentID, &a.Tags, &a.UpdatedAt); err != nil {
			return result, err
		}
		a.UpdatedAt = a.UpdatedAt.UTC()
		result.Items = append(result.Items, a)
	}
	result.NextOffset = nextOffset(offset, limit, result.Total)
	return result, rows.Err()
}

func getArtifact(ctx context.Context, u *unit, in GetInput) (Artifact, error) {
	key, err := id(in.ID)
	if err != nil {
		return Artifact{}, err
	}
	return readArtifact(ctx, u, key, false)
}

// readArtifact reads one artifact; the audit trail of a removed one needs includeDeleted.
func readArtifact(ctx context.Context, u *unit, key string, includeDeleted bool) (Artifact, error) {
	var a Artifact
	var params, sources []byte
	q := `SELECT ` + summaryColumns + `,params,sources FROM life.artifacts WHERE id=$1`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	if err := u.tx.QueryRow(ctx, q, key).Scan(&a.ID, &a.Title, &a.Template, &a.Version, &a.AgentID, &a.Tags, &a.UpdatedAt, &params, &sources); err != nil {
		return a, err
	}
	a.UpdatedAt = a.UpdatedAt.UTC()
	a.Params = params
	return a, json.Unmarshal(sources, &a.Sources)
}

func createArtifact(ctx context.Context, u *unit, in CreateArtifactInput) (Artifact, error) {
	if err := validateArtifact(in.ArtifactData); err != nil {
		return Artifact{}, err
	}
	key, err := createID("")
	if err != nil {
		return Artifact{}, err
	}
	if in.Sources == nil {
		in.Sources = []ArtifactSource{}
	}
	sources, err := json.Marshal(in.Sources)
	if err != nil {
		return Artifact{}, err
	}
	a := Artifact{
		ArtifactSummary: ArtifactSummary{
			ID: key, Title: in.Title, Template: in.Template,
			AgentID: artifactCreator(ctx), Tags: normalizeArtifactTags(in.Tags),
		},
		Params: in.Params, Sources: in.Sources,
	}
	err = u.tx.QueryRow(ctx, `INSERT INTO life.artifacts(id,title,template,params,sources,agent_id,tags)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7) RETURNING version,updated_at`,
		key, a.Title, a.Template, []byte(a.Params), sources, a.AgentID, a.Tags).Scan(&a.Version, &a.UpdatedAt)
	if err != nil {
		return Artifact{}, err
	}
	a.UpdatedAt = a.UpdatedAt.UTC()
	return a, u.audit(ctx, "artifact", a.ID, nil, a)
}

func updateArtifact(ctx context.Context, u *unit, in UpdateArtifactInput) (Artifact, error) {
	if err := validateArtifact(in.ArtifactData); err != nil {
		return Artifact{}, err
	}
	old, err := getArtifact(ctx, u, GetInput{ID: in.ID})
	if err != nil {
		return Artifact{}, err
	}
	if err = version(in.ExpectedVersion, old.Version); err != nil {
		return Artifact{}, err
	}
	if in.Sources == nil {
		in.Sources = []ArtifactSource{}
	}
	sources, err := json.Marshal(in.Sources)
	if err != nil {
		return Artifact{}, err
	}
	a := old
	a.Title, a.Template, a.Params, a.Sources = in.Title, in.Template, in.Params, in.Sources
	if in.Tags != nil {
		a.Tags = normalizeArtifactTags(*in.Tags)
	}
	err = u.tx.QueryRow(ctx, `UPDATE life.artifacts
		SET title=$2,template=$3,params=$4,sources=$5,tags=$6,version=version+1,updated_at=now()
		WHERE id=$1 RETURNING version,updated_at`,
		a.ID, a.Title, a.Template, []byte(a.Params), sources, a.Tags).Scan(&a.Version, &a.UpdatedAt)
	if err != nil {
		return Artifact{}, err
	}
	a.UpdatedAt = a.UpdatedAt.UTC()
	return a, u.audit(ctx, "artifact", a.ID, old, a)
}

func deleteArtifact(ctx context.Context, u *unit, in EditInput) (Artifact, error) {
	k, e := id(in.ID)
	if e != nil {
		return Artifact{}, e
	}
	var deleted bool
	if e = u.tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM life.artifacts WHERE id=$1 FOR UPDATE`, k).Scan(&deleted); e != nil {
		return Artifact{}, e
	}
	if deleted {
		return Artifact{}, conflict("The artifact was already removed")
	}
	old, e := getArtifact(ctx, u, GetInput{ID: k})
	if e != nil {
		return old, e
	}
	if e = version(in.ExpectedVersion, old.Version); e != nil {
		return old, e
	}
	if _, e = u.tx.Exec(ctx, `UPDATE life.artifacts SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, old.ID); e != nil {
		return old, e
	}
	// The live read hides it now; read the row directly for the audit trail.
	after, e := readArtifact(ctx, u, old.ID, true)
	if e != nil {
		return after, e
	}
	return after, u.audit(ctx, "artifact", after.ID, old, after)
}
