package engine

import (
	"context"
	"encoding/json"
	"strings"
)

type ArtifactData struct {
	Title     string     `json:"title"`
	Mode      string     `json:"mode" jsonschema:"document or deck; each page of a deck is a slide"`
	CatalogID string     `json:"catalog_id"`
	Pages     []ViewPage `json:"pages"`
}

type Artifact struct {
	ID string `json:"id"`
	ArtifactData
	Version int64 `json:"version"`
}

type CreateArtifactInput struct {
	Meta
	ArtifactData
}

type UpdateArtifactInput struct {
	EditInput
	ArtifactData
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

func getArtifact(ctx context.Context, u *unit, in GetInput) (Artifact, error) {
	var a Artifact
	key, err := id(in.ID)
	if err != nil {
		return a, err
	}
	var pages []byte
	err = u.tx.QueryRow(ctx, `SELECT id::text,title,mode,catalog_id,pages,version FROM life.artifacts WHERE id=$1`, key).
		Scan(&a.ID, &a.Title, &a.Mode, &a.CatalogID, &pages, &a.Version)
	if err != nil {
		return a, err
	}
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
	if _, err = u.tx.Exec(ctx, `INSERT INTO life.artifacts(id,title,mode,catalog_id,pages) VALUES($1,$2,$3,$4,$5)`,
		key, in.Title, in.Mode, in.CatalogID, pages); err != nil {
		return Artifact{}, err
	}
	a := Artifact{ID: key, ArtifactData: in.ArtifactData, Version: 1}
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
	if _, err = u.tx.Exec(ctx, `UPDATE life.artifacts SET title=$2,mode=$3,catalog_id=$4,pages=$5,version=version+1 WHERE id=$1`,
		old.ID, in.Title, in.Mode, in.CatalogID, pages); err != nil {
		return Artifact{}, err
	}
	a := Artifact{ID: old.ID, ArtifactData: in.ArtifactData, Version: old.Version + 1}
	return a, u.audit(ctx, "artifact", a.ID, old, a)
}
