package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

type ExchangeMetadataBackfillOptions struct {
	DryRun bool
}

type ExchangeMetadataBackfillResult struct {
	Scanned         int  `json:"scanned"`
	UpdatedModel    int  `json:"updated_model"`
	UpdatedEntry    int  `json:"updated_entry"`
	LegacyOrUnknown int  `json:"legacy_or_unknown"`
	MissingCassette int  `json:"missing_cassette"`
	Conflicts       int  `json:"conflicts"`
	DryRun          bool `json:"dry_run"`
}

type exchangeBackfillRow struct {
	ID               string
	ResponseID       string
	RequestAuditID   string
	TraceID          string
	ExchangeID       string
	ExchangeKind     string
	ExchangeRole     string
	ParentExchangeID string
	SequenceIndex    sql.NullInt64
	CassettePath     string
	Model            string
	Endpoint         string
}

type exchangeBackfillInference struct {
	responseID               string
	requestAuditID           string
	traceID                  string
	exchangeID               string
	exchangeKind             string
	exchangeRole             string
	parentExchangeID         string
	sequenceIndex            int
	hasSequenceIndex         bool
	legacyOrUnknown          bool
	missingCassette          bool
	cassetteProvidedMetadata bool
}

func (s *Store) BackfillExchangeMetadata(ctx context.Context, opts ExchangeMetadataBackfillOptions) (ExchangeMetadataBackfillResult, error) {
	result := ExchangeMetadataBackfillResult{DryRun: opts.DryRun}
	if s == nil || s.db == nil {
		return result, errors.New("backfill exchange metadata: store is not initialized")
	}
	rows, err := s.loadExchangeBackfillRows(ctx)
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		result.Scanned++
		inferred, err := s.inferExchangeBackfill(ctx, row)
		if err != nil {
			return result, err
		}
		if inferred.legacyOrUnknown {
			result.LegacyOrUnknown++
		}
		if inferred.missingCassette {
			result.MissingCassette++
		}
		patch, conflicts := buildExchangeBackfillPatch(row, inferred)
		result.Conflicts += conflicts
		if !patch.changed {
			continue
		}
		kind := patch.finalKind
		if kind == "" {
			kind = normalizeBackfillText(row.ExchangeKind)
		}
		switch kind {
		case "entry":
			result.UpdatedEntry++
		case "model":
			result.UpdatedModel++
		}
		if opts.DryRun {
			continue
		}
		if err := s.applyExchangeBackfillPatch(ctx, row.ID, patch); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) loadExchangeBackfillRows(ctx context.Context) ([]exchangeBackfillRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, response_id, request_audit_id, trace_id, exchange_id, exchange_kind, exchange_role,
			parent_exchange_id, sequence_index, cassette_path, model, endpoint
		FROM upstream_exchanges
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []exchangeBackfillRow
	for rows.Next() {
		var row exchangeBackfillRow
		var responseID, requestAuditID, traceID, exchangeID, exchangeKind, exchangeRole, parentExchangeID sql.NullString
		var cassettePath, model, endpoint sql.NullString
		if err := rows.Scan(&row.ID, &responseID, &requestAuditID, &traceID, &exchangeID, &exchangeKind, &exchangeRole, &parentExchangeID, &row.SequenceIndex, &cassettePath, &model, &endpoint); err != nil {
			return nil, err
		}
		row.ResponseID = responseID.String
		row.RequestAuditID = requestAuditID.String
		row.TraceID = traceID.String
		row.ExchangeID = exchangeID.String
		row.ExchangeKind = exchangeKind.String
		row.ExchangeRole = exchangeRole.String
		row.ParentExchangeID = parentExchangeID.String
		row.CassettePath = cassettePath.String
		row.Model = model.String
		row.Endpoint = endpoint.String
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) inferExchangeBackfill(ctx context.Context, row exchangeBackfillRow) (exchangeBackfillInference, error) {
	inferred := exchangeBackfillInference{}
	if row.CassettePath != "" {
		meta, isV3, found, err := s.exchangeMetadataFromCassette(row.CassettePath)
		if err != nil {
			return inferred, err
		}
		switch {
		case found && isV3:
			applyMetaToExchangeInference(&inferred, meta)
			inferred.cassetteProvidedMetadata = true
		case found:
			inferred.legacyOrUnknown = true
		default:
			inferred.missingCassette = true
		}
	}
	if !inferred.cassetteProvidedMetadata {
		meta, found, err := s.exchangeMetadataFromLog(ctx, row.CassettePath)
		if err != nil {
			return inferred, err
		}
		if found {
			applyMetaToExchangeInference(&inferred, meta)
		} else if row.CassettePath == "" {
			inferred.legacyOrUnknown = true
		}
	}
	if inferred.requestAuditID == "" && row.RequestAuditID == "" && inferred.responseID != "" {
		if requestAuditID, found, err := s.uniqueRequestAuditIDForResponse(ctx, inferred.responseID); err != nil {
			return inferred, err
		} else if found {
			inferred.requestAuditID = requestAuditID
		}
	}
	if inferred.responseID == "" && row.ResponseID == "" && inferred.requestAuditID != "" {
		if responseID, found, err := s.responseIDForRequestAudit(ctx, inferred.requestAuditID); err != nil {
			return inferred, err
		} else if found {
			inferred.responseID = responseID
		}
	}
	if inferred.exchangeKind == "" {
		inferred.exchangeKind = inferExchangeKindFromRow(row)
		if inferred.exchangeKind == "" {
			inferred.legacyOrUnknown = true
		}
	}
	if inferred.exchangeRole == "" {
		switch inferred.exchangeKind {
		case "entry":
			inferred.exchangeRole = "client_request"
		case "model":
			inferred.exchangeRole = "primary_model_call"
		}
	}
	return inferred, nil
}

func (s *Store) exchangeMetadataFromCassette(path string) (recordfile.MetaData, bool, bool, error) {
	resolved := s.resolveCassettePath(path)
	content, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return recordfile.MetaData{}, false, false, nil
		}
		return recordfile.MetaData{}, false, false, fmt.Errorf("read cassette %s: %w", resolved, err)
	}
	isV3 := strings.HasPrefix(string(content), recordfile.FileMagic+"\n")
	prelude, err := recordfile.ParsePrelude(content)
	if err != nil {
		return recordfile.MetaData{}, isV3, true, nil
	}
	return prelude.Header.Meta, isV3, true, nil
}

func (s *Store) resolveCassettePath(path string) string {
	if filepath.IsAbs(path) || s.outputDir == "" {
		return path
	}
	return filepath.Join(s.outputDir, path)
}

func (s *Store) exchangeMetadataFromLog(ctx context.Context, cassettePath string) (recordfile.MetaData, bool, error) {
	if cassettePath == "" {
		return recordfile.MetaData{}, false, nil
	}
	meta := recordfile.MetaData{}
	var traceID, exchangeID, exchangeKind, exchangeRole, parentExchangeID, endpoint, model sql.NullString
	var sequenceIndex sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT trace_id, exchange_id, exchange_kind, exchange_role, parent_exchange_id, sequence_index, endpoint, model
		FROM logs
		WHERE path = ?
	`, s.resolveCassettePath(cassettePath)).Scan(&traceID, &exchangeID, &exchangeKind, &exchangeRole, &parentExchangeID, &sequenceIndex, &endpoint, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return recordfile.MetaData{}, false, nil
	}
	if err != nil {
		return recordfile.MetaData{}, false, err
	}
	meta.TraceID = traceID.String
	meta.ExchangeID = exchangeID.String
	meta.ExchangeKind = exchangeKind.String
	meta.ExchangeRole = exchangeRole.String
	meta.ParentExchangeID = parentExchangeID.String
	if sequenceIndex.Valid {
		meta.SequenceIndex = int(sequenceIndex.Int64)
	}
	meta.Endpoint = endpoint.String
	meta.Model = model.String
	return meta, true, nil
}

func (s *Store) uniqueRequestAuditIDForResponse(ctx context.Context, responseID string) (string, bool, error) {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return "", false, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM request_audits WHERE response_id = ? ORDER BY created_at, id LIMIT 2`, responseID)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if len(ids) != 1 {
		return "", false, nil
	}
	return ids[0], true, nil
}

func (s *Store) responseIDForRequestAudit(ctx context.Context, requestAuditID string) (string, bool, error) {
	requestAuditID = strings.TrimSpace(requestAuditID)
	if requestAuditID == "" {
		return "", false, nil
	}
	var responseID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT response_id FROM request_audits WHERE id = ?`, requestAuditID).Scan(&responseID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return responseID.String, responseID.Valid && strings.TrimSpace(responseID.String) != "", nil
}

func applyMetaToExchangeInference(inferred *exchangeBackfillInference, meta recordfile.MetaData) {
	inferred.responseID = firstBackfillNonEmpty(inferred.responseID, meta.ResponseID)
	inferred.requestAuditID = firstBackfillNonEmpty(inferred.requestAuditID, meta.RequestAuditID)
	inferred.traceID = firstBackfillNonEmpty(inferred.traceID, meta.TraceID)
	inferred.exchangeID = firstBackfillNonEmpty(inferred.exchangeID, meta.ExchangeID)
	inferred.exchangeKind = firstBackfillNonEmpty(inferred.exchangeKind, meta.ExchangeKind)
	inferred.exchangeRole = firstBackfillNonEmpty(inferred.exchangeRole, meta.ExchangeRole)
	inferred.parentExchangeID = firstBackfillNonEmpty(inferred.parentExchangeID, meta.ParentExchangeID)
	if !inferred.hasSequenceIndex && meta.SequenceIndex != 0 {
		inferred.sequenceIndex = meta.SequenceIndex
		inferred.hasSequenceIndex = true
	}
}

func inferExchangeKindFromRow(row exchangeBackfillRow) string {
	endpoint := normalizeBackfillText(row.Endpoint)
	if endpoint == "" {
		return "model"
	}
	semantics := llm.ClassifyPath(endpoint, "")
	if semantics.Operation != "" || semantics.Endpoint != "" || strings.HasPrefix(endpoint, "/v1/") {
		return "model"
	}
	return ""
}

type exchangeBackfillPatch struct {
	responseID       string
	requestAuditID   string
	traceID          string
	exchangeID       string
	exchangeKind     string
	exchangeRole     string
	parentExchangeID string
	sequenceIndex    int
	hasSequenceIndex bool
	changed          bool
	finalKind        string
}

func buildExchangeBackfillPatch(row exchangeBackfillRow, inferred exchangeBackfillInference) (exchangeBackfillPatch, int) {
	var patch exchangeBackfillPatch
	var conflicts int
	patch.responseID, conflicts = patchStringIfMissing(row.ResponseID, inferred.responseID, conflicts)
	patch.requestAuditID, conflicts = patchStringIfMissing(row.RequestAuditID, inferred.requestAuditID, conflicts)
	patch.traceID, conflicts = patchStringIfMissing(row.TraceID, inferred.traceID, conflicts)
	patch.exchangeID, conflicts = patchStringIfMissing(row.ExchangeID, inferred.exchangeID, conflicts)
	patch.exchangeKind, conflicts = patchStringIfMissing(row.ExchangeKind, inferred.exchangeKind, conflicts)
	kindConflict := normalizeBackfillText(row.ExchangeKind) != "" && normalizeBackfillText(inferred.exchangeKind) != "" && normalizeBackfillText(row.ExchangeKind) != normalizeBackfillText(inferred.exchangeKind)
	if !kindConflict {
		patch.exchangeRole, conflicts = patchStringIfMissing(row.ExchangeRole, inferred.exchangeRole, conflicts)
	} else if normalizeBackfillText(row.ExchangeRole) != "" && normalizeBackfillText(inferred.exchangeRole) != "" && normalizeBackfillText(row.ExchangeRole) != normalizeBackfillText(inferred.exchangeRole) {
		conflicts++
	}
	patch.parentExchangeID, conflicts = patchStringIfMissing(row.ParentExchangeID, inferred.parentExchangeID, conflicts)
	if inferred.hasSequenceIndex {
		if row.SequenceIndex.Valid {
			if int(row.SequenceIndex.Int64) != inferred.sequenceIndex {
				conflicts++
			}
		} else {
			patch.sequenceIndex = inferred.sequenceIndex
			patch.hasSequenceIndex = true
		}
	}
	patch.changed = patch.responseID != "" || patch.requestAuditID != "" || patch.traceID != "" ||
		patch.exchangeID != "" || patch.exchangeKind != "" || patch.exchangeRole != "" ||
		patch.parentExchangeID != "" || patch.hasSequenceIndex
	patch.finalKind = normalizeBackfillText(row.ExchangeKind)
	if patch.exchangeKind != "" {
		patch.finalKind = patch.exchangeKind
	}
	return patch, conflicts
}

func patchStringIfMissing(current string, inferred string, conflicts int) (string, int) {
	current = normalizeBackfillText(current)
	inferred = normalizeBackfillText(inferred)
	if inferred == "" {
		return "", conflicts
	}
	if current == "" {
		return inferred, conflicts
	}
	if current != inferred {
		conflicts++
	}
	return "", conflicts
}

func (s *Store) applyExchangeBackfillPatch(ctx context.Context, id string, patch exchangeBackfillPatch) error {
	sets := []string{}
	args := []any{}
	addString := func(column string, value string) {
		if value == "" {
			return
		}
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	addString("response_id", patch.responseID)
	addString("request_audit_id", patch.requestAuditID)
	addString("trace_id", patch.traceID)
	addString("exchange_id", patch.exchangeID)
	addString("exchange_kind", patch.exchangeKind)
	addString("exchange_role", patch.exchangeRole)
	addString("parent_exchange_id", patch.parentExchangeID)
	if patch.hasSequenceIndex {
		sets = append(sets, "sequence_index = ?")
		args = append(args, patch.sequenceIndex)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	query := "UPDATE upstream_exchanges SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func normalizeBackfillText(value string) string {
	return strings.TrimSpace(value)
}

func firstBackfillNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
