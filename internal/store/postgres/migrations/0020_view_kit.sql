-- Views are now composed from the view kit (docs: tracker_catalog_get): blocks have a `kind`, not the old `type`.
-- A view with any block in the old shape cannot be read as the new one, so it is retired; the goal shows no view
-- until the assistant composes a new one.
UPDATE life.views SET deleted_at = now(), version = version + 1, updated_at = now()
WHERE deleted_at IS NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(blocks) b WHERE NOT (b ? 'kind'));
