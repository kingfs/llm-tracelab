package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kingfs/llm-tracelab/ent/dao"
	"github.com/kingfs/llm-tracelab/ent/dao/channelconfig"
	"github.com/kingfs/llm-tracelab/ent/dao/channelmodel"
	"github.com/kingfs/llm-tracelab/ent/dao/channelproberun"
	"github.com/kingfs/llm-tracelab/ent/dao/dataset"
	"github.com/kingfs/llm-tracelab/ent/dao/datasetexample"
	"github.com/kingfs/llm-tracelab/ent/dao/evalrun"
	"github.com/kingfs/llm-tracelab/ent/dao/experimentrun"
	"github.com/kingfs/llm-tracelab/ent/dao/modelcatalog"
	"github.com/kingfs/llm-tracelab/ent/dao/predicate"
	"github.com/kingfs/llm-tracelab/ent/dao/score"
	"github.com/kingfs/llm-tracelab/ent/dao/tracelog"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreammodel"
	"github.com/kingfs/llm-tracelab/ent/dao/upstreamtarget"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	_ "modernc.org/sqlite"
)

type LogEntry struct {
	ID              string
	Header          recordfile.RecordHeader
	LogPath         string
	SessionID       string
	SessionSource   string
	WindowID        string
	ClientRequestID string
}

type Stats struct {
	TotalRequest   int
	AvgTTFT        int
	TotalTokens    int
	SuccessRequest int
	FailedRequest  int
	SuccessRate    float64
}

type Store struct {
	db        *sql.DB
	client    *dao.Client
	outputDir string
	dbPath    string
	secrets   *secretBox
	syncMu    sync.Mutex
}

const (
	localSecretKeyFile = "trace_index.secret"
	secretEnvelopeV1   = "tlsec:v1:"
)

type secretBox struct {
	aead        cipher.AEAD
	mode        string
	keyPath     string
	fingerprint string
}

type SecretStatus struct {
	Mode        string `json:"mode"`
	KeyPath     string `json:"key_path"`
	Exists      bool   `json:"exists"`
	Readable    bool   `json:"readable"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ListPageResult struct {
	Items      []LogEntry
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

type ListFilter struct {
	Query    string
	Provider string
	Model    string
}

type GroupingInfo struct {
	SessionID       string
	SessionSource   string
	WindowID        string
	ClientRequestID string
}

type SessionSummary struct {
	SessionID      string
	SessionSource  string
	RequestCount   int
	FirstSeen      time.Time
	LastSeen       time.Time
	LastModel      string
	Providers      []string
	SuccessRequest int
	FailedRequest  int
	SuccessRate    float64
	TotalTokens    int
	AvgTTFT        int
	TotalDuration  int64
	StreamCount    int
}

type SessionPageResult struct {
	Items      []SessionSummary
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

type UpstreamTargetRecord struct {
	ID                string
	BaseURL           string
	ProviderPreset    string
	ProtocolFamily    string
	RoutingProfile    string
	Enabled           bool
	Priority          int
	Weight            float64
	CapacityHint      float64
	LastRefreshAt     time.Time
	LastRefreshStatus string
	LastRefreshError  string
}

type UpstreamModelRecord struct {
	UpstreamID string
	Model      string
	Source     string
	SeenAt     time.Time
}

type ChannelConfigRecord struct {
	ID                 string
	Name               string
	Description        string
	BaseURL            string
	ProviderPreset     string
	ProtocolFamily     string
	RoutingProfile     string
	APIVersion         string
	Deployment         string
	Project            string
	Location           string
	ModelResource      string
	APIKeyCiphertext   []byte
	APIKeyHint         string
	HeadersJSON        string
	Enabled            bool
	Priority           int
	Weight             float64
	CapacityHint       float64
	ModelDiscovery     string
	AllowUnknownModels bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastProbeAt        time.Time
	LastProbeStatus    string
	LastProbeError     string
}

type ChannelModelRecord struct {
	ChannelID               string
	Model                   string
	DisplayName             string
	Source                  string
	Enabled                 bool
	SupportsResponses       *int
	SupportsChatCompletions *int
	SupportsEmbeddings      *int
	ContextWindow           *int
	InputModalitiesJSON     string
	OutputModalitiesJSON    string
	RawModelJSON            string
	FirstSeenAt             time.Time
	LastSeenAt              time.Time
	LastProbeAt             time.Time
}

type ModelCatalogRecord struct {
	Model       string
	DisplayName string
	Family      string
	Vendor      string
	Description string
	TagsJSON    string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	LastUsedAt  time.Time
}

type ChannelProbeRunRecord struct {
	ID                 string
	ChannelID          string
	Status             string
	StartedAt          time.Time
	CompletedAt        time.Time
	DurationMs         int64
	DiscoveredCount    int
	EnabledCount       int
	Endpoint           string
	StatusCode         int
	ErrorText          string
	RequestMetaJSON    string
	ResponseSampleJSON string
}

type UpstreamAnalyticsRecord struct {
	UpstreamID     string
	RequestCount   int
	SuccessRequest int
	FailedRequest  int
	SuccessRate    float64
	TotalTokens    int
	AvgTTFT        int
	LastSeen       time.Time
	Models         []string
	LastModel      string
	RecentErrors   []string
	RecentFailures []UpstreamFailureRecord
}

type UpstreamFailureRecord struct {
	TraceID    string
	Model      string
	Endpoint   string
	StatusCode int
	RecordedAt time.Time
	Reason     string
	ErrorText  string
}

type UpstreamDetail struct {
	Analytics      UpstreamAnalyticsRecord
	Traces         []LogEntry
	Models         []CountItem
	Endpoints      []CountItem
	FailureReasons []CountItem
	Timeline       []TimeCountItem
}

type CountItem struct {
	Label string
	Count int
}

type RoutingFailureAnalytics struct {
	Total    int
	Reasons  []CountItem
	Recent   []RoutingFailureRecord
	Timeline []TimeCountItem
}

type RoutingFailureRecord struct {
	TraceID    string
	Model      string
	Endpoint   string
	RecordedAt time.Time
	Reason     string
	ErrorText  string
	StatusCode int
}

type AnalysisRunRecord struct {
	ID              int64
	TraceID         string
	SessionID       string
	Kind            string
	Analyzer        string
	AnalyzerVersion string
	Model           string
	InputRef        string
	OutputJSON      string
	Status          string
	CreatedAt       time.Time
}

type TimeCountItem struct {
	Time  time.Time
	Count int
}

type UsageSummaryRecord struct {
	RequestCount     int
	SuccessRequest   int
	FailedRequest    int
	SuccessRate      float64
	TotalTokens      int
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	AvgTTFT          int
	AvgDurationMs    int64
	LastSeen         time.Time
}

type UsageTrendRecord struct {
	Time          time.Time
	RequestCount  int
	FailedRequest int
	TotalTokens   int
	ModelCount    int
}

type ModelCatalogAnalyticsRecord struct {
	Model               string
	DisplayName         string
	ProviderCount       int
	ChannelCount        int
	EnabledChannelCount int
	Summary             UsageSummaryRecord
	Today               UsageSummaryRecord
	Channels            []string
}

type ModelDetailAnalyticsRecord struct {
	Model    ModelCatalogAnalyticsRecord
	Trends   []UsageTrendRecord
	Channels []ChannelModelAnalyticsRecord
}

type ChannelModelAnalyticsRecord struct {
	ChannelID string
	Model     string
	Enabled   bool
	Source    string
	Summary   UsageSummaryRecord
}

type DatasetRecord struct {
	ID           string
	Name         string
	Description  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExampleCount int
}

type DatasetExampleRecord struct {
	DatasetID  string
	TraceID    string
	Position   int
	AddedAt    time.Time
	SourceType string
	SourceID   string
	Note       string
	Trace      LogEntry
}

type EvalRunRecord struct {
	ID           string
	DatasetID    string
	SourceType   string
	SourceID     string
	EvaluatorSet string
	CreatedAt    time.Time
	CompletedAt  time.Time
	TraceCount   int
	ScoreCount   int
	PassCount    int
	FailCount    int
}

type ScoreRecord struct {
	ID           string
	TraceID      string
	SessionID    string
	DatasetID    string
	EvalRunID    string
	EvaluatorKey string
	Value        float64
	Status       string
	Label        string
	Explanation  string
	CreatedAt    time.Time
}

type ExperimentRunRecord struct {
	ID                  string
	Name                string
	Description         string
	BaselineEvalRunID   string
	CandidateEvalRunID  string
	CreatedAt           time.Time
	BaselineScoreCount  int
	CandidateScoreCount int
	BaselinePassRate    float64
	CandidatePassRate   float64
	PassRateDelta       float64
	MatchedScoreCount   int
	ImprovementCount    int
	RegressionCount     int
}

type ObservationSummary struct {
	TraceID       string
	Parser        string
	ParserVersion string
	Status        string
	Provider      string
	Operation     string
	Model         string
	SummaryJSON   string
	WarningsJSON  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ParseJobRecord struct {
	ID        int64
	TraceID   string
	Status    string
	Attempts  int
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type FindingFilter struct {
	Category string
	Severity string
}

type ScoreFilter struct {
	TraceID   string
	SessionID string
	DatasetID string
	EvalRunID string
}

func (s *Store) ListUpstreamTargets() ([]UpstreamTargetRecord, error) {
	rows, err := s.client.UpstreamTarget.Query().
		Order(upstreamtarget.ByPriority(entsql.OrderDesc()), upstreamtarget.ByID()).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	out := make([]UpstreamTargetRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, upstreamTargetRecordFromEnt(row))
	}
	return out, nil
}

func (s *Store) ListUpstreamModels() ([]UpstreamModelRecord, error) {
	rows, err := s.client.UpstreamModel.Query().
		Order(upstreammodel.ByUpstreamID(), upstreammodel.ByModel()).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	out := make([]UpstreamModelRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, upstreamModelRecordFromEnt(row))
	}
	return out, nil
}

func (s *Store) ListChannelConfigs() ([]ChannelConfigRecord, error) {
	rows, err := s.client.ChannelConfig.Query().
		Order(channelconfig.ByPriority(entsql.OrderDesc()), channelconfig.ByID()).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	out := make([]ChannelConfigRecord, 0, len(rows))
	for _, row := range rows {
		record, err := s.channelConfigRecordFromEnt(row)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *Store) GetChannelConfig(channelID string) (ChannelConfigRecord, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return ChannelConfigRecord{}, fmt.Errorf("channel id is required")
	}
	row, err := s.client.ChannelConfig.Get(context.Background(), channelID)
	if err != nil {
		if dao.IsNotFound(err) {
			return ChannelConfigRecord{}, sql.ErrNoRows
		}
		return ChannelConfigRecord{}, err
	}
	return s.channelConfigRecordFromEnt(row)
}

func (s *Store) UpsertChannelConfig(record ChannelConfigRecord) (ChannelConfigRecord, error) {
	record.ID = strings.TrimSpace(record.ID)
	record.Name = strings.TrimSpace(record.Name)
	record.BaseURL = strings.TrimSpace(record.BaseURL)
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	if record.Name == "" {
		return ChannelConfigRecord{}, fmt.Errorf("channel name is required")
	}
	if record.BaseURL == "" {
		return ChannelConfigRecord{}, fmt.Errorf("channel base_url is required")
	}
	if strings.TrimSpace(record.HeadersJSON) == "" {
		record.HeadersJSON = "{}"
	}
	if strings.TrimSpace(record.ModelDiscovery) == "" {
		record.ModelDiscovery = "list_models"
	}
	if record.Weight == 0 {
		record.Weight = 1
	}
	if record.CapacityHint == 0 {
		record.CapacityHint = 1
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	storedAPIKey, err := s.encryptSecretBytes(record.APIKeyCiphertext)
	if err != nil {
		return ChannelConfigRecord{}, err
	}
	storedHeaders, err := s.encryptHeadersJSON(record.HeadersJSON)
	if err != nil {
		return ChannelConfigRecord{}, err
	}

	create := s.client.ChannelConfig.Create().
		SetID(record.ID).
		SetName(record.Name).
		SetDescription(strings.TrimSpace(record.Description)).
		SetBaseURL(record.BaseURL).
		SetProviderPreset(strings.TrimSpace(record.ProviderPreset)).
		SetProtocolFamily(strings.TrimSpace(record.ProtocolFamily)).
		SetRoutingProfile(strings.TrimSpace(record.RoutingProfile)).
		SetAPIVersion(strings.TrimSpace(record.APIVersion)).
		SetDeployment(strings.TrimSpace(record.Deployment)).
		SetProject(strings.TrimSpace(record.Project)).
		SetLocation(strings.TrimSpace(record.Location)).
		SetModelResource(strings.TrimSpace(record.ModelResource)).
		SetAPIKeyHint(strings.TrimSpace(record.APIKeyHint)).
		SetHeadersJSON(storedHeaders).
		SetEnabled(record.Enabled).
		SetPriority(record.Priority).
		SetWeight(record.Weight).
		SetCapacityHint(record.CapacityHint).
		SetModelDiscovery(record.ModelDiscovery).
		SetAllowUnknownModels(record.AllowUnknownModels).
		SetCreatedAt(record.CreatedAt).
		SetUpdatedAt(record.UpdatedAt).
		SetLastProbeStatus(strings.TrimSpace(record.LastProbeStatus)).
		SetLastProbeError(strings.TrimSpace(record.LastProbeError))
	if len(storedAPIKey) > 0 {
		create.SetAPIKeyCiphertext(storedAPIKey)
	}
	if !record.LastProbeAt.IsZero() {
		create.SetLastProbeAt(record.LastProbeAt.UTC())
	}
	if err := create.
		OnConflictColumns(channelconfig.FieldID).
		UpdateNewValues().
		Exec(context.Background()); err != nil {
		return ChannelConfigRecord{}, err
	}
	return s.GetChannelConfig(record.ID)
}

func (s *Store) SetChannelEnabled(channelID string, enabled bool) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return fmt.Errorf("channel id is required")
	}
	return s.client.ChannelConfig.UpdateOneID(channelID).
		SetEnabled(enabled).
		SetUpdatedAt(time.Now().UTC()).
		Exec(context.Background())
}

func (s *Store) UpdateChannelProbeStatus(channelID string, probedAt time.Time, status string, errorText string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return fmt.Errorf("channel id is required")
	}
	update := s.client.ChannelConfig.UpdateOneID(channelID).
		SetUpdatedAt(time.Now().UTC()).
		SetLastProbeStatus(strings.TrimSpace(status)).
		SetLastProbeError(strings.TrimSpace(errorText))
	if !probedAt.IsZero() {
		update.SetLastProbeAt(probedAt.UTC())
	}
	return update.Exec(context.Background())
}

func (s *Store) ListChannelModels(channelID string, enabledOnly bool) ([]ChannelModelRecord, error) {
	query := s.client.ChannelModel.Query()
	var predicates []predicate.ChannelModel
	if channelID = strings.TrimSpace(channelID); channelID != "" {
		predicates = append(predicates, channelmodel.ChannelIDEQ(channelID))
	}
	if enabledOnly {
		predicates = append(predicates, channelmodel.EnabledEQ(true))
	}
	if len(predicates) > 0 {
		query = query.Where(predicates...)
	}
	rows, err := query.Order(channelmodel.ByChannelID(), channelmodel.ByModel()).All(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]ChannelModelRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelModelRecordFromEnt(row))
	}
	return out, nil
}

func (s *Store) ReplaceChannelModels(channelID string, records []ChannelModelRecord) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return fmt.Errorf("channel id is required")
	}

	ctx := context.Background()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ChannelModel.Delete().Where(channelmodel.ChannelIDEQ(channelID)).Exec(ctx); err != nil {
		return err
	}
	creates := make([]*dao.ChannelModelCreate, 0, len(records))
	now := time.Now().UTC()
	for _, record := range records {
		model := strings.ToLower(strings.TrimSpace(record.Model))
		if model == "" {
			continue
		}
		firstSeenAt := record.FirstSeenAt
		if firstSeenAt.IsZero() {
			firstSeenAt = now
		}
		lastSeenAt := record.LastSeenAt
		if lastSeenAt.IsZero() {
			lastSeenAt = now
		}
		enabled := record.Enabled
		create := tx.ChannelModel.Create().
			SetChannelID(channelID).
			SetModel(model).
			SetDisplayName(strings.TrimSpace(record.DisplayName)).
			SetSource(strings.TrimSpace(record.Source)).
			SetEnabled(enabled).
			SetNillableSupportsResponses(record.SupportsResponses).
			SetNillableSupportsChatCompletions(record.SupportsChatCompletions).
			SetNillableSupportsEmbeddings(record.SupportsEmbeddings).
			SetNillableContextWindow(record.ContextWindow).
			SetInputModalitiesJSON(defaultJSON(record.InputModalitiesJSON, "[]")).
			SetOutputModalitiesJSON(defaultJSON(record.OutputModalitiesJSON, "[]")).
			SetRawModelJSON(defaultJSON(record.RawModelJSON, "{}")).
			SetFirstSeenAt(firstSeenAt.UTC()).
			SetLastSeenAt(lastSeenAt.UTC())
		if !record.LastProbeAt.IsZero() {
			create.SetLastProbeAt(record.LastProbeAt.UTC())
		}
		creates = append(creates, create)
	}
	if len(creates) > 0 {
		if err := tx.ChannelModel.CreateBulk(creates...).
			OnConflictColumns(channelmodel.FieldChannelID, channelmodel.FieldModel).
			UpdateNewValues().
			Exec(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertChannelModel(channelID string, record ChannelModelRecord) (ChannelModelRecord, error) {
	channelID = strings.TrimSpace(channelID)
	model := strings.ToLower(strings.TrimSpace(record.Model))
	if channelID == "" {
		return ChannelModelRecord{}, fmt.Errorf("channel id is required")
	}
	if model == "" {
		return ChannelModelRecord{}, fmt.Errorf("model is required")
	}
	now := time.Now().UTC()
	if record.FirstSeenAt.IsZero() {
		record.FirstSeenAt = now
	}
	if record.LastSeenAt.IsZero() {
		record.LastSeenAt = now
	}
	source := strings.TrimSpace(record.Source)
	if source == "" {
		source = "manual"
	}
	create := s.client.ChannelModel.Create().
		SetChannelID(channelID).
		SetModel(model).
		SetDisplayName(strings.TrimSpace(record.DisplayName)).
		SetSource(source).
		SetEnabled(record.Enabled).
		SetNillableSupportsResponses(record.SupportsResponses).
		SetNillableSupportsChatCompletions(record.SupportsChatCompletions).
		SetNillableSupportsEmbeddings(record.SupportsEmbeddings).
		SetNillableContextWindow(record.ContextWindow).
		SetInputModalitiesJSON(defaultJSON(record.InputModalitiesJSON, "[]")).
		SetOutputModalitiesJSON(defaultJSON(record.OutputModalitiesJSON, "[]")).
		SetRawModelJSON(defaultJSON(record.RawModelJSON, "{}")).
		SetFirstSeenAt(record.FirstSeenAt.UTC()).
		SetLastSeenAt(record.LastSeenAt.UTC())
	if !record.LastProbeAt.IsZero() {
		create.SetLastProbeAt(record.LastProbeAt.UTC())
	}
	if err := create.
		OnConflictColumns(channelmodel.FieldChannelID, channelmodel.FieldModel).
		UpdateNewValues().
		Exec(context.Background()); err != nil {
		return ChannelModelRecord{}, err
	}
	if err := s.UpsertModelCatalog(ModelCatalogRecord{
		Model:       model,
		DisplayName: strings.TrimSpace(record.DisplayName),
		FirstSeenAt: record.FirstSeenAt,
		LastSeenAt:  record.LastSeenAt,
	}); err != nil {
		return ChannelModelRecord{}, err
	}
	row, err := s.client.ChannelModel.Query().
		Where(channelmodel.ChannelIDEQ(channelID), channelmodel.ModelEQ(model)).
		Only(context.Background())
	if err != nil {
		return ChannelModelRecord{}, err
	}
	return channelModelRecordFromEnt(row), nil
}

func (s *Store) SetChannelModelEnabled(channelID string, model string, enabled bool) error {
	channelID = strings.TrimSpace(channelID)
	model = strings.ToLower(strings.TrimSpace(model))
	if channelID == "" {
		return fmt.Errorf("channel id is required")
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}
	_, err := s.client.ChannelModel.Update().
		Where(channelmodel.ChannelIDEQ(channelID), channelmodel.ModelEQ(model)).
		SetEnabled(enabled).
		SetLastSeenAt(time.Now().UTC()).
		Save(context.Background())
	return err
}

func (s *Store) UpsertModelCatalog(record ModelCatalogRecord) error {
	model := strings.ToLower(strings.TrimSpace(record.Model))
	if model == "" {
		return fmt.Errorf("model is required")
	}
	now := time.Now().UTC()
	if record.FirstSeenAt.IsZero() {
		record.FirstSeenAt = now
	}
	if record.LastSeenAt.IsZero() {
		record.LastSeenAt = now
	}
	create := s.client.ModelCatalog.Create().
		SetID(model).
		SetDisplayName(strings.TrimSpace(record.DisplayName)).
		SetFamily(strings.TrimSpace(record.Family)).
		SetVendor(strings.TrimSpace(record.Vendor)).
		SetDescription(strings.TrimSpace(record.Description)).
		SetTagsJSON(defaultJSON(record.TagsJSON, "[]")).
		SetFirstSeenAt(record.FirstSeenAt.UTC()).
		SetLastSeenAt(record.LastSeenAt.UTC())
	if !record.LastUsedAt.IsZero() {
		create.SetLastUsedAt(record.LastUsedAt.UTC())
	}
	return create.
		OnConflictColumns(modelcatalog.FieldID).
		UpdateNewValues().
		Exec(context.Background())
}

func (s *Store) CreateChannelProbeRun(record ChannelProbeRunRecord) (ChannelProbeRunRecord, error) {
	record.ID = strings.TrimSpace(record.ID)
	record.ChannelID = strings.TrimSpace(record.ChannelID)
	record.Status = strings.TrimSpace(record.Status)
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	if record.ChannelID == "" {
		return ChannelProbeRunRecord{}, fmt.Errorf("channel id is required")
	}
	if record.Status == "" {
		return ChannelProbeRunRecord{}, fmt.Errorf("probe status is required")
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}
	create := s.client.ChannelProbeRun.Create().
		SetID(record.ID).
		SetChannelID(record.ChannelID).
		SetStatus(record.Status).
		SetStartedAt(record.StartedAt.UTC()).
		SetDurationMs(record.DurationMs).
		SetDiscoveredCount(record.DiscoveredCount).
		SetEnabledCount(record.EnabledCount).
		SetEndpoint(strings.TrimSpace(record.Endpoint)).
		SetStatusCode(record.StatusCode).
		SetErrorText(strings.TrimSpace(record.ErrorText)).
		SetRequestMetaJSON(defaultJSON(record.RequestMetaJSON, "{}")).
		SetResponseSampleJSON(defaultJSON(record.ResponseSampleJSON, "{}"))
	if !record.CompletedAt.IsZero() {
		create.SetCompletedAt(record.CompletedAt.UTC())
	}
	if err := create.Exec(context.Background()); err != nil {
		return ChannelProbeRunRecord{}, err
	}
	return record, nil
}

func (s *Store) ListChannelProbeRuns(channelID string, limit int) ([]ChannelProbeRunRecord, error) {
	query := s.client.ChannelProbeRun.Query()
	if channelID = strings.TrimSpace(channelID); channelID != "" {
		query = query.Where(channelproberun.ChannelIDEQ(channelID))
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := query.
		Order(channelproberun.ByStartedAt(entsql.OrderDesc()), channelproberun.ByID(entsql.OrderDesc())).
		Limit(limit).
		All(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]ChannelProbeRunRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelProbeRunRecordFromEnt(row))
	}
	return out, nil
}

func (s *Store) ListModelCatalogAnalytics(since time.Time, todaySince time.Time) ([]ModelCatalogAnalyticsRecord, error) {
	modelSet := map[string]*ModelCatalogAnalyticsRecord{}
	channelModels, err := s.ListChannelModels("", false)
	if err != nil {
		return nil, err
	}
	providersByModel := map[string]map[string]struct{}{}
	channelsByModel := map[string]map[string]struct{}{}
	enabledChannelsByModel := map[string]map[string]struct{}{}
	for _, channelModel := range channelModels {
		model := strings.ToLower(strings.TrimSpace(channelModel.Model))
		if model == "" {
			continue
		}
		record := modelSet[model]
		if record == nil {
			record = &ModelCatalogAnalyticsRecord{Model: model, DisplayName: channelModel.DisplayName}
			modelSet[model] = record
		}
		if providersByModel[model] == nil {
			providersByModel[model] = map[string]struct{}{}
			channelsByModel[model] = map[string]struct{}{}
			enabledChannelsByModel[model] = map[string]struct{}{}
		}
		channelsByModel[model][channelModel.ChannelID] = struct{}{}
		if channelModel.Enabled {
			enabledChannelsByModel[model][channelModel.ChannelID] = struct{}{}
		}
	}

	channels, err := s.ListChannelConfigs()
	if err != nil {
		return nil, err
	}
	providerByChannel := map[string]string{}
	for _, channel := range channels {
		providerByChannel[channel.ID] = channel.ProviderPreset
	}
	for model, channelIDs := range channelsByModel {
		for channelID := range channelIDs {
			if provider := providerByChannel[channelID]; provider != "" {
				providersByModel[model][provider] = struct{}{}
			}
		}
	}

	logModels, err := s.listLogModels(since)
	if err != nil {
		return nil, err
	}
	for _, model := range logModels {
		if modelSet[model] == nil {
			modelSet[model] = &ModelCatalogAnalyticsRecord{Model: model}
		}
	}
	for _, record := range modelSet {
		summary, err := s.usageSummary("model = ?", []any{record.Model}, since)
		if err != nil {
			return nil, err
		}
		today, err := s.usageSummary("model = ?", []any{record.Model}, todaySince)
		if err != nil {
			return nil, err
		}
		record.Summary = summary
		record.Today = today
		record.ProviderCount = len(providersByModel[record.Model])
		record.ChannelCount = len(channelsByModel[record.Model])
		record.EnabledChannelCount = len(enabledChannelsByModel[record.Model])
		record.Channels = sortedKeys(channelsByModel[record.Model])
	}

	out := make([]ModelCatalogAnalyticsRecord, 0, len(modelSet))
	for _, record := range modelSet {
		out = append(out, *record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Summary.RequestCount != out[j].Summary.RequestCount {
			return out[i].Summary.RequestCount > out[j].Summary.RequestCount
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

func (s *Store) GetModelDetailAnalytics(model string, since time.Time, todaySince time.Time, bucketSize time.Duration, bucketCount int) (ModelDetailAnalyticsRecord, error) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ModelDetailAnalyticsRecord{}, fmt.Errorf("model is required")
	}
	all, err := s.ListModelCatalogAnalytics(since, todaySince)
	if err != nil {
		return ModelDetailAnalyticsRecord{}, err
	}
	var detail ModelDetailAnalyticsRecord
	for _, item := range all {
		if item.Model == model {
			detail.Model = item
			break
		}
	}
	if detail.Model.Model == "" {
		return ModelDetailAnalyticsRecord{}, sql.ErrNoRows
	}
	trends, err := s.usageTrends("model = ?", []any{model}, since, bucketSize, bucketCount)
	if err != nil {
		return ModelDetailAnalyticsRecord{}, err
	}
	detail.Trends = trends

	channelModels, err := s.ListChannelModels("", false)
	if err != nil {
		return ModelDetailAnalyticsRecord{}, err
	}
	for _, channelModel := range channelModels {
		if strings.ToLower(channelModel.Model) != model {
			continue
		}
		summary, err := s.usageSummary("model = ? AND selected_upstream_id = ?", []any{model, channelModel.ChannelID}, since)
		if err != nil {
			return ModelDetailAnalyticsRecord{}, err
		}
		detail.Channels = append(detail.Channels, ChannelModelAnalyticsRecord{
			ChannelID: channelModel.ChannelID,
			Model:     model,
			Enabled:   channelModel.Enabled,
			Source:    channelModel.Source,
			Summary:   summary,
		})
	}
	sort.Slice(detail.Channels, func(i, j int) bool {
		if detail.Channels[i].Summary.RequestCount != detail.Channels[j].Summary.RequestCount {
			return detail.Channels[i].Summary.RequestCount > detail.Channels[j].Summary.RequestCount
		}
		return detail.Channels[i].ChannelID < detail.Channels[j].ChannelID
	})
	return detail, nil
}

func (s *Store) GetChannelUsageTrends(channelID string, since time.Time, bucketSize time.Duration, bucketCount int) ([]UsageTrendRecord, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, fmt.Errorf("channel id is required")
	}
	return s.usageTrends("selected_upstream_id = ?", []any{channelID}, since, bucketSize, bucketCount)
}

func (s *Store) GetChannelUsageSummary(channelID string, since time.Time) (UsageSummaryRecord, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return UsageSummaryRecord{}, fmt.Errorf("channel id is required")
	}
	return s.usageSummary("selected_upstream_id = ?", []any{channelID}, since)
}

func (s *Store) GetChannelModelUsage(channelID string, since time.Time) ([]ChannelModelAnalyticsRecord, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, fmt.Errorf("channel id is required")
	}
	channelModels, err := s.ListChannelModels(channelID, false)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := make([]ChannelModelAnalyticsRecord, 0, len(channelModels))
	for _, channelModel := range channelModels {
		model := strings.ToLower(strings.TrimSpace(channelModel.Model))
		if model == "" {
			continue
		}
		seen[model] = struct{}{}
		summary, err := s.usageSummary("selected_upstream_id = ? AND model = ?", []any{channelID, model}, since)
		if err != nil {
			return nil, err
		}
		out = append(out, ChannelModelAnalyticsRecord{
			ChannelID: channelID,
			Model:     model,
			Enabled:   channelModel.Enabled,
			Source:    channelModel.Source,
			Summary:   summary,
		})
	}

	logModels, err := s.channelLogModels(channelID, since)
	if err != nil {
		return nil, err
	}
	for _, model := range logModels {
		if _, ok := seen[model]; ok {
			continue
		}
		summary, err := s.usageSummary("selected_upstream_id = ? AND model = ?", []any{channelID, model}, since)
		if err != nil {
			return nil, err
		}
		out = append(out, ChannelModelAnalyticsRecord{
			ChannelID: channelID,
			Model:     model,
			Source:    "trace",
			Summary:   summary,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Summary.TotalTokens != out[j].Summary.TotalTokens {
			return out[i].Summary.TotalTokens > out[j].Summary.TotalTokens
		}
		if out[i].Summary.RequestCount != out[j].Summary.RequestCount {
			return out[i].Summary.RequestCount > out[j].Summary.RequestCount
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

func (s *Store) GetChannelRecentFailures(channelID string, since time.Time, limit int) ([]UpstreamFailureRecord, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, fmt.Errorf("channel id is required")
	}
	return s.upstreamRecentFailures(channelID, limit, since, "")
}

func (s *Store) ListUpstreamAnalytics(limitModels int, limitErrors int, since time.Time, modelFilter string) ([]UpstreamAnalyticsRecord, error) {
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	rows, err := s.db.Query(`
		SELECT
			selected_upstream_id,
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS success_request,
			COALESCE(SUM(CASE WHEN status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS failed_request,
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				100.0 * SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*)
			END AS success_rate,
			COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN total_tokens ELSE 0 END), 0) AS total_tokens,
			COALESCE(AVG(CASE WHEN status_code BETWEEN 200 AND 299 THEN ttft_ms END), 0) AS avg_ttft,
			MAX(recorded_at) AS last_seen
		FROM logs
		WHERE selected_upstream_id <> ''`+whereSQL+`
		GROUP BY selected_upstream_id
		ORDER BY request_count DESC, selected_upstream_id ASC
	`, whereArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UpstreamAnalyticsRecord
	for rows.Next() {
		var (
			record   UpstreamAnalyticsRecord
			lastSeen string
			avgTTFT  float64
		)
		if err := rows.Scan(
			&record.UpstreamID,
			&record.RequestCount,
			&record.SuccessRequest,
			&record.FailedRequest,
			&record.SuccessRate,
			&record.TotalTokens,
			&avgTTFT,
			&lastSeen,
		); err != nil {
			return nil, err
		}
		record.AvgTTFT = int(math.Round(avgTTFT))
		record.LastSeen, err = timeParse(lastSeen)
		if err != nil {
			return nil, err
		}
		record.Models, record.LastModel, err = s.upstreamModelCoverage(record.UpstreamID, limitModels, since, modelFilter)
		if err != nil {
			return nil, err
		}
		record.RecentErrors, err = s.upstreamRecentErrors(record.UpstreamID, limitErrors, since, modelFilter)
		if err != nil {
			return nil, err
		}
		record.RecentFailures, err = s.upstreamRecentFailures(record.UpstreamID, limitErrors, since, modelFilter)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) GetRoutingFailureAnalytics(since time.Time, modelFilter string, limitReasons int, limitRecent int, bucketSize time.Duration, bucketCount int) (RoutingFailureAnalytics, error) {
	if limitReasons <= 0 {
		limitReasons = 5
	}
	if limitRecent <= 0 {
		limitRecent = 5
	}
	if bucketSize <= 0 {
		bucketSize = time.Hour
	}
	if bucketCount <= 0 {
		bucketCount = 12
	}

	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	baseWhere := `routing_failure_reason <> ''`
	if strings.TrimSpace(whereSQL) != "" {
		baseWhere += whereSQL
	}

	var analytics RoutingFailureAnalytics
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM logs WHERE `+baseWhere, whereArgs...).Scan(&analytics.Total); err != nil {
		return RoutingFailureAnalytics{}, err
	}

	reasonArgs := append([]any{}, whereArgs...)
	reasonArgs = append(reasonArgs, limitReasons)
	reasonRows, err := s.db.Query(`
		SELECT routing_failure_reason, COUNT(*) AS count
		FROM logs
		WHERE `+baseWhere+`
		GROUP BY routing_failure_reason
		ORDER BY count DESC, routing_failure_reason ASC
		LIMIT ?
	`, reasonArgs...)
	if err != nil {
		return RoutingFailureAnalytics{}, err
	}
	defer reasonRows.Close()
	for reasonRows.Next() {
		var item CountItem
		if err := reasonRows.Scan(&item.Label, &item.Count); err != nil {
			return RoutingFailureAnalytics{}, err
		}
		analytics.Reasons = append(analytics.Reasons, item)
	}
	if err := reasonRows.Err(); err != nil {
		return RoutingFailureAnalytics{}, err
	}

	recentArgs := append([]any{}, whereArgs...)
	recentArgs = append(recentArgs, limitRecent)
	recentRows, err := s.db.Query(`
		SELECT trace_id, model, endpoint, recorded_at, routing_failure_reason, error_text, status_code
		FROM logs
		WHERE `+baseWhere+`
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, recentArgs...)
	if err != nil {
		return RoutingFailureAnalytics{}, err
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var (
			item       RoutingFailureRecord
			recordedAt string
		)
		if err := recentRows.Scan(&item.TraceID, &item.Model, &item.Endpoint, &recordedAt, &item.Reason, &item.ErrorText, &item.StatusCode); err != nil {
			return RoutingFailureAnalytics{}, err
		}
		item.RecordedAt, err = timeParse(recordedAt)
		if err != nil {
			return RoutingFailureAnalytics{}, err
		}
		analytics.Recent = append(analytics.Recent, item)
	}
	if err := recentRows.Err(); err != nil {
		return RoutingFailureAnalytics{}, err
	}

	referenceTime := time.Now().UTC()
	var latestRecordedAt string
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(recorded_at), '') FROM logs WHERE `+baseWhere, whereArgs...).Scan(&latestRecordedAt); err != nil {
		return RoutingFailureAnalytics{}, err
	}
	if strings.TrimSpace(latestRecordedAt) != "" {
		latestTime, err := timeParse(latestRecordedAt)
		if err != nil {
			return RoutingFailureAnalytics{}, err
		}
		referenceTime = latestTime
	}
	bucketStart := referenceTime.UTC().Truncate(bucketSize).Add(-time.Duration(bucketCount-1) * bucketSize)
	timelineArgs := append([]any{bucketStart.Format(timeLayout)}, whereArgs...)
	timelineRows, err := s.db.Query(`
		SELECT recorded_at
		FROM logs
		WHERE recorded_at >= ? AND `+baseWhere+`
		ORDER BY recorded_at ASC
	`, timelineArgs...)
	if err != nil {
		return RoutingFailureAnalytics{}, err
	}
	defer timelineRows.Close()

	buckets := make(map[time.Time]int, bucketCount)
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		buckets[slot] = 0
	}
	for timelineRows.Next() {
		var recordedAt string
		if err := timelineRows.Scan(&recordedAt); err != nil {
			return RoutingFailureAnalytics{}, err
		}
		recordedTime, err := timeParse(recordedAt)
		if err != nil {
			return RoutingFailureAnalytics{}, err
		}
		slot := recordedTime.UTC().Truncate(bucketSize)
		if slot.Before(bucketStart) {
			continue
		}
		if _, ok := buckets[slot]; ok {
			buckets[slot]++
		}
	}
	if err := timelineRows.Err(); err != nil {
		return RoutingFailureAnalytics{}, err
	}
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		analytics.Timeline = append(analytics.Timeline, TimeCountItem{
			Time:  slot,
			Count: buckets[slot],
		})
	}

	return analytics, nil
}

func (s *Store) GetUpstreamDetail(upstreamID string, since time.Time, modelFilter string, traceLimit int, bucketSize time.Duration, bucketCount int) (UpstreamDetail, error) {
	if traceLimit <= 0 {
		traceLimit = 50
	}
	if bucketSize <= 0 {
		bucketSize = 2 * time.Hour
	}
	if bucketCount <= 0 {
		bucketCount = 12
	}
	analytics, err := s.ListUpstreamAnalytics(8, 5, since, modelFilter)
	if err != nil {
		return UpstreamDetail{}, err
	}
	var detail UpstreamDetail
	for _, item := range analytics {
		if item.UpstreamID == upstreamID {
			detail.Analytics = item
			break
		}
	}
	if detail.Analytics.UpstreamID == "" {
		return UpstreamDetail{}, sql.ErrNoRows
	}

	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	queryArgs := append([]any{upstreamID}, whereArgs...)
	queryArgs = append(queryArgs, traceLimit)
	rows, err := s.db.Query(`
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		FROM logs
		WHERE selected_upstream_id = ?`+whereSQL+`
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, queryArgs...)
	if err != nil {
		return UpstreamDetail{}, err
	}
	defer rows.Close()

	modelCounts := map[string]int{}
	endpointCounts := map[string]int{}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return UpstreamDetail{}, err
		}
		detail.Traces = append(detail.Traces, entry)
		if entry.Header.Meta.Model != "" {
			modelCounts[entry.Header.Meta.Model]++
		}
		if entry.Header.Meta.Endpoint != "" {
			endpointCounts[entry.Header.Meta.Endpoint]++
		}
	}
	if err := rows.Err(); err != nil {
		return UpstreamDetail{}, err
	}
	detail.Models = sortedCountItems(modelCounts)
	detail.Endpoints = sortedCountItems(endpointCounts)
	detail.FailureReasons, err = s.upstreamFailureReasons(upstreamID, 5, since, modelFilter)
	if err != nil {
		return UpstreamDetail{}, err
	}

	timelineArgs := append([]any{upstreamID}, whereArgs...)
	var latestRecordedAt string
	err = s.db.QueryRow(`
		SELECT COALESCE(MAX(recorded_at), '')
		FROM logs
		WHERE selected_upstream_id = ? AND status_code >= 400`+whereSQL,
		timelineArgs...,
	).Scan(&latestRecordedAt)
	if err != nil {
		return UpstreamDetail{}, err
	}
	referenceTime := time.Now().UTC()
	if latestRecordedAt != "" {
		latestTime, err := timeParse(latestRecordedAt)
		if err != nil {
			return UpstreamDetail{}, err
		}
		referenceTime = latestTime
	}
	bucketStart := referenceTime.Truncate(bucketSize).Add(-time.Duration(bucketCount-1) * bucketSize)
	buckets := make(map[time.Time]int, bucketCount)
	failureTimelineArgs := append([]any{upstreamID}, whereArgs...)
	timelineRows, err := s.db.Query(`
		SELECT recorded_at
		FROM logs
		WHERE selected_upstream_id = ? AND status_code >= 400`+whereSQL+`
		ORDER BY recorded_at ASC
	`, failureTimelineArgs...)
	if err != nil {
		return UpstreamDetail{}, err
	}
	defer timelineRows.Close()
	for timelineRows.Next() {
		var recordedAt string
		if err := timelineRows.Scan(&recordedAt); err != nil {
			return UpstreamDetail{}, err
		}
		recordedTime, err := timeParse(recordedAt)
		if err != nil {
			return UpstreamDetail{}, err
		}
		slot := recordedTime.UTC().Truncate(bucketSize)
		if slot.Before(bucketStart) {
			continue
		}
		if slot.After(referenceTime.Truncate(bucketSize)) {
			continue
		}
		if _, ok := buckets[slot]; ok {
			buckets[slot]++
			continue
		}
		buckets[slot] = 1
	}
	if err := timelineRows.Err(); err != nil {
		return UpstreamDetail{}, err
	}
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		detail.Timeline = append(detail.Timeline, TimeCountItem{
			Time:  slot,
			Count: buckets[slot],
		})
	}
	return detail, nil
}

func (s *Store) upstreamModelCoverage(upstreamID string, limit int, since time.Time, modelFilter string) ([]string, string, error) {
	if limit <= 0 {
		limit = 5
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT model, COUNT(*) AS count
		FROM logs
		WHERE selected_upstream_id = ? AND model <> ''`+whereSQL+`
		GROUP BY model
		ORDER BY count DESC, model ASC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var models []string
	for rows.Next() {
		var model string
		var count int
		if err := rows.Scan(&model, &count); err != nil {
			return nil, "", err
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var lastModel string
	lastModelArgs := append([]any{upstreamID}, whereArgs...)
	if err := s.db.QueryRow(`
		SELECT model
		FROM logs
		WHERE selected_upstream_id = ? AND model <> ''`+whereSQL+`
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT 1
	`, lastModelArgs...).Scan(&lastModel); err != nil && err != sql.ErrNoRows {
		return nil, "", err
	}

	return models, lastModel, nil
}

func (s *Store) upstreamRecentErrors(upstreamID string, limit int, since time.Time, modelFilter string) ([]string, error) {
	if limit <= 0 {
		limit = 3
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT error_text, status_code, endpoint
		FROM logs
		WHERE selected_upstream_id = ?
		  `+whereSQL+`
		  AND (status_code NOT BETWEEN 200 AND 299 OR error_text <> '')
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var (
			errorText  string
			statusCode int
			endpoint   string
		)
		if err := rows.Scan(&errorText, &statusCode, &endpoint); err != nil {
			return nil, err
		}
		switch {
		case strings.TrimSpace(errorText) != "":
			out = append(out, errorText)
		case strings.TrimSpace(endpoint) != "":
			out = append(out, fmt.Sprintf("%s HTTP %d", endpoint, statusCode))
		default:
			out = append(out, fmt.Sprintf("HTTP %d", statusCode))
		}
	}
	return out, rows.Err()
}

func (s *Store) upstreamRecentFailures(upstreamID string, limit int, since time.Time, modelFilter string) ([]UpstreamFailureRecord, error) {
	if limit <= 0 {
		limit = 3
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT trace_id, model, endpoint, status_code, recorded_at, error_text
		FROM logs
		WHERE selected_upstream_id = ?
		  `+whereSQL+`
		  AND (status_code NOT BETWEEN 200 AND 299 OR error_text <> '')
		ORDER BY recorded_at DESC, trace_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UpstreamFailureRecord
	for rows.Next() {
		var (
			record     UpstreamFailureRecord
			recordedAt string
		)
		if err := rows.Scan(&record.TraceID, &record.Model, &record.Endpoint, &record.StatusCode, &recordedAt, &record.ErrorText); err != nil {
			return nil, err
		}
		record.RecordedAt, err = timeParse(recordedAt)
		if err != nil {
			return nil, err
		}
		record.Reason = classifyUpstreamFailure(record.StatusCode, record.ErrorText)
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) upstreamFailureReasons(upstreamID string, limit int, since time.Time, modelFilter string) ([]CountItem, error) {
	if limit <= 0 {
		limit = 5
	}
	whereSQL, whereArgs := buildUpstreamAnalyticsWhere(since, modelFilter)
	args := append([]any{upstreamID}, whereArgs...)
	rows, err := s.db.Query(`
		SELECT status_code, error_text
		FROM logs
		WHERE selected_upstream_id = ?
		  `+whereSQL+`
		  AND (status_code NOT BETWEEN 200 AND 299 OR error_text <> '')
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var (
			statusCode int
			errorText  string
		)
		if err := rows.Scan(&statusCode, &errorText); err != nil {
			return nil, err
		}
		counts[classifyUpstreamFailure(statusCode, errorText)]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	items := sortedCountItems(counts)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func classifyUpstreamFailure(statusCode int, errorText string) string {
	text := strings.ToLower(strings.TrimSpace(errorText))
	switch {
	case statusCode == 408 || statusCode == 504 || strings.Contains(text, "timeout") || strings.Contains(text, "timed out") || strings.Contains(text, "deadline exceeded") || strings.Contains(text, "context deadline exceeded"):
		return "timeout"
	case statusCode == 429 || strings.Contains(text, "rate limit") || strings.Contains(text, "too many requests"):
		return "rate_limited"
	case statusCode == 401 || statusCode == 403 || strings.Contains(text, "unauthorized") || strings.Contains(text, "forbidden") || strings.Contains(text, "invalid api key") || strings.Contains(text, "authentication"):
		return "auth_denied"
	case statusCode == 503 || strings.Contains(text, "overloaded") || strings.Contains(text, "overload") || strings.Contains(text, "capacity") || strings.Contains(text, "unavailable"):
		return "upstream_overloaded"
	case statusCode >= 500:
		return "upstream_error"
	case statusCode >= 400:
		return "request_rejected"
	case text != "":
		return "transport_error"
	default:
		return "unknown_failure"
	}
}

func buildUpstreamAnalyticsWhere(since time.Time, modelFilter string) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if !since.IsZero() {
		clauses = append(clauses, `recorded_at >= ?`)
		args = append(args, since.UTC().Format(timeLayout))
	}
	if model := strings.TrimSpace(modelFilter); model != "" {
		clauses = append(clauses, `LOWER(model) LIKE LOWER(?) ESCAPE '\'`)
		args = append(args, "%"+escapeLike(model)+"%")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

func (s *Store) listLogModels(since time.Time) ([]string, error) {
	where := "model <> ''"
	args := []any{}
	if !since.IsZero() {
		where += " AND recorded_at >= ?"
		args = append(args, since.UTC().Format(timeLayout))
	}
	rows, err := s.db.Query(`SELECT DISTINCT LOWER(model) FROM logs WHERE `+where+` ORDER BY LOWER(model)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, err
		}
		if model = strings.TrimSpace(model); model != "" {
			out = append(out, model)
		}
	}
	return out, rows.Err()
}

func (s *Store) channelLogModels(channelID string, since time.Time) ([]string, error) {
	where := "selected_upstream_id = ? AND model <> ''"
	args := []any{channelID}
	if !since.IsZero() {
		where += " AND recorded_at >= ?"
		args = append(args, since.UTC().Format(timeLayout))
	}
	rows, err := s.db.Query(`SELECT DISTINCT LOWER(model) FROM logs WHERE `+where+` ORDER BY LOWER(model)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, err
		}
		if model = strings.TrimSpace(model); model != "" {
			out = append(out, model)
		}
	}
	return out, rows.Err()
}

func (s *Store) usageSummary(baseWhere string, baseArgs []any, since time.Time) (UsageSummaryRecord, error) {
	where := strings.TrimSpace(baseWhere)
	if where == "" {
		where = "1=1"
	}
	args := append([]any(nil), baseArgs...)
	if !since.IsZero() {
		where += " AND recorded_at >= ?"
		args = append(args, since.UTC().Format(timeLayout))
	}
	var (
		record       UsageSummaryRecord
		avgTTFT      float64
		avgDuration  float64
		successRate  float64
		lastSeenText string
	)
	if err := s.db.QueryRow(`
		SELECT
			COUNT(*) AS request_count,
			COALESCE(SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS success_request,
			COALESCE(SUM(CASE WHEN status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS failed_request,
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE 100.0 * SUM(CASE WHEN status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*) END AS success_rate,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
			COALESCE(AVG(ttft_ms), 0) AS avg_ttft,
			COALESCE(AVG(duration_ms), 0) AS avg_duration_ms,
			COALESCE(MAX(recorded_at), '') AS last_seen
		FROM logs
		WHERE `+where, args...).Scan(
		&record.RequestCount,
		&record.SuccessRequest,
		&record.FailedRequest,
		&successRate,
		&record.TotalTokens,
		&record.PromptTokens,
		&record.CompletionTokens,
		&record.CachedTokens,
		&avgTTFT,
		&avgDuration,
		&lastSeenText,
	); err != nil {
		return UsageSummaryRecord{}, err
	}
	record.SuccessRate = successRate
	record.AvgTTFT = int(math.Round(avgTTFT))
	record.AvgDurationMs = int64(math.Round(avgDuration))
	if strings.TrimSpace(lastSeenText) != "" {
		lastSeen, err := timeParse(lastSeenText)
		if err != nil {
			return UsageSummaryRecord{}, err
		}
		record.LastSeen = lastSeen
	}
	return record, nil
}

func (s *Store) usageTrends(baseWhere string, baseArgs []any, since time.Time, bucketSize time.Duration, bucketCount int) ([]UsageTrendRecord, error) {
	if bucketSize <= 0 {
		bucketSize = 24 * time.Hour
	}
	if bucketCount <= 0 {
		bucketCount = 7
	}
	where := strings.TrimSpace(baseWhere)
	if where == "" {
		where = "1=1"
	}
	args := append([]any(nil), baseArgs...)
	referenceTime := time.Now().UTC()
	var latestRecordedAt string
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(recorded_at), '') FROM logs WHERE `+where, args...).Scan(&latestRecordedAt); err != nil {
		return nil, err
	}
	if strings.TrimSpace(latestRecordedAt) != "" {
		latestTime, err := timeParse(latestRecordedAt)
		if err != nil {
			return nil, err
		}
		referenceTime = latestTime.UTC()
	}
	if !since.IsZero() {
		where += " AND recorded_at >= ?"
		args = append(args, since.UTC().Format(timeLayout))
	}
	bucketStart := referenceTime.Truncate(bucketSize).Add(-time.Duration(bucketCount-1) * bucketSize)
	queryArgs := append([]any(nil), args...)
	queryArgs = append(queryArgs, bucketStart.Format(timeLayout))
	rows, err := s.db.Query(`
		SELECT recorded_at, status_code, total_tokens, model
		FROM logs
		WHERE `+where+` AND recorded_at >= ?
		ORDER BY recorded_at ASC
	`, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type bucket struct {
		requests int
		failed   int
		tokens   int
		models   map[string]struct{}
	}
	buckets := make(map[time.Time]*bucket, bucketCount)
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		buckets[slot] = &bucket{models: map[string]struct{}{}}
	}
	for rows.Next() {
		var (
			recordedAt  string
			statusCode  int
			totalTokens int
			model       string
		)
		if err := rows.Scan(&recordedAt, &statusCode, &totalTokens, &model); err != nil {
			return nil, err
		}
		recordedTime, err := timeParse(recordedAt)
		if err != nil {
			return nil, err
		}
		slot := recordedTime.UTC().Truncate(bucketSize)
		item := buckets[slot]
		if item == nil {
			continue
		}
		item.requests++
		if statusCode < 200 || statusCode >= 300 {
			item.failed++
		}
		item.tokens += totalTokens
		if model = strings.TrimSpace(model); model != "" {
			item.models[strings.ToLower(model)] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]UsageTrendRecord, 0, bucketCount)
	for index := 0; index < bucketCount; index++ {
		slot := bucketStart.Add(time.Duration(index) * bucketSize)
		item := buckets[slot]
		out = append(out, UsageTrendRecord{
			Time:          slot,
			RequestCount:  item.requests,
			FailedRequest: item.failed,
			TotalTokens:   item.tokens,
			ModelCount:    len(item.models),
		})
	}
	return out, nil
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedCountItems(counts map[string]int) []CountItem {
	items := make([]CountItem, 0, len(counts))
	for label, count := range counts {
		items = append(items, CountItem{Label: label, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Label < items[j].Label
	})
	return items
}

func New(outputDir string) (*Store, error) {
	return NewWithDatabase(outputDir, "sqlite", filepath.Join(outputDir, "trace_index.sqlite3"), 4, 4)
}

func NewWithDatabase(outputDir string, driver string, dsn string, maxOpenConns int, maxIdleConns int) (*Store, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}
	secrets, err := newLocalSecretBox(outputDir)
	if err != nil {
		return nil, err
	}
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" {
		return nil, fmt.Errorf("store driver %q is not supported yet", driver)
	}
	dbPath := config.SQLitePathFromDSN(dsn)
	if strings.TrimSpace(dbPath) == "" {
		dbPath = filepath.Join(outputDir, "llm_tracelab.sqlite3")
	}
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, err
	}
	if maxOpenConns > 0 {
		db.SetMaxOpenConns(maxOpenConns)
	}
	if maxIdleConns > 0 {
		db.SetMaxIdleConns(maxIdleConns)
	}

	st := &Store{
		db:        db,
		client:    dao.NewClient(dao.Driver(entsql.OpenDB(dialect.SQLite, db))),
		outputDir: outputDir,
		dbPath:    dbPath,
		secrets:   secrets,
	}
	if err := st.initSchema(); err != nil {
		_ = st.Close()
		return nil, err
	}

	return st, nil
}

func newLocalSecretBox(outputDir string) (*secretBox, error) {
	if strings.TrimSpace(outputDir) == "" {
		outputDir = "."
	}
	keyPath := filepath.Join(outputDir, localSecretKeyFile)
	key, err := readOrCreateLocalSecretKey(keyPath)
	if err != nil {
		return nil, err
	}
	box, err := secretBoxFromKey(key, keyPath)
	if err != nil {
		return nil, err
	}
	return box, nil
}

func readOrCreateLocalSecretKey(keyPath string) ([]byte, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		encoded := []byte(base64.RawStdEncoding.EncodeToString(key) + "\n")
		if err := os.WriteFile(keyPath, encoded, 0o600); err != nil {
			return nil, err
		}
	} else {
		decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(key)))
		if err != nil {
			return nil, fmt.Errorf("decode local secret key: %w", err)
		}
		key = decoded
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("local secret key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

func secretBoxFromKey(key []byte, keyPath string) (*secretBox, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("local secret key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key)
	return &secretBox{
		aead:        aead,
		mode:        "encrypted-local",
		keyPath:     keyPath,
		fingerprint: hex.EncodeToString(sum[:8]),
	}, nil
}

func (s *Store) SecretStorageMode() string {
	if s == nil || s.secrets == nil || s.secrets.aead == nil {
		return "plaintext-local"
	}
	return s.secrets.mode
}

func (s *Store) SecretStatus() SecretStatus {
	status := SecretStatus{
		Mode: s.SecretStorageMode(),
	}
	if s == nil || s.secrets == nil {
		status.Error = "secret box is not configured"
		return status
	}
	status.KeyPath = s.secrets.keyPath
	status.Fingerprint = s.secrets.fingerprint
	if strings.TrimSpace(status.KeyPath) == "" {
		status.Readable = s.secrets.aead != nil
		return status
	}
	key, err := readLocalSecretKey(status.KeyPath)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Exists = true
	status.Readable = true
	sum := sha256.Sum256(key)
	status.Fingerprint = hex.EncodeToString(sum[:8])
	return status
}

func (s *Store) ExportLocalSecretKey() ([]byte, SecretStatus, error) {
	status := s.SecretStatus()
	if strings.TrimSpace(status.KeyPath) == "" {
		return nil, status, fmt.Errorf("local secret key path is not configured")
	}
	key, err := readLocalSecretKey(status.KeyPath)
	if err != nil {
		return nil, status, err
	}
	encoded := []byte(base64.RawStdEncoding.EncodeToString(key) + "\n")
	status.Exists = true
	status.Readable = true
	sum := sha256.Sum256(key)
	status.Fingerprint = hex.EncodeToString(sum[:8])
	return encoded, status, nil
}

func readLocalSecretKey(keyPath string) ([]byte, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(keyData)))
	if err != nil {
		return nil, fmt.Errorf("decode local secret key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("local secret key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

func (s *Store) encryptSecretBytes(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	if bytes.HasPrefix(plaintext, []byte(secretEnvelopeV1)) {
		return append([]byte(nil), plaintext...), nil
	}
	if s == nil || s.secrets == nil || s.secrets.aead == nil {
		return append([]byte(nil), plaintext...), nil
	}
	nonce := make([]byte, s.secrets.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := s.secrets.aead.Seal(nil, nonce, plaintext, nil)
	payload := append(nonce, sealed...)
	out := secretEnvelopeV1 + base64.RawStdEncoding.EncodeToString(payload)
	return []byte(out), nil
}

func (s *Store) decryptSecretBytes(value []byte) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	if !bytes.HasPrefix(value, []byte(secretEnvelopeV1)) {
		return append([]byte(nil), value...), nil
	}
	if s == nil || s.secrets == nil || s.secrets.aead == nil {
		return nil, fmt.Errorf("encrypted local secret cannot be decrypted without a local key")
	}
	encoded := strings.TrimPrefix(string(value), secretEnvelopeV1)
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	nonceSize := s.secrets.aead.NonceSize()
	if len(payload) < nonceSize {
		return nil, fmt.Errorf("encrypted local secret payload is too short")
	}
	nonce, ciphertext := payload[:nonceSize], payload[nonceSize:]
	return s.secrets.aead.Open(nil, nonce, ciphertext, nil)
}

func (s *Store) encryptHeadersJSON(headersJSON string) (string, error) {
	headersJSON = strings.TrimSpace(headersJSON)
	if headersJSON == "" {
		return "{}", nil
	}
	headers := map[string]string{}
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		return "", err
	}
	for key, value := range headers {
		if !isSecretChannelHeader(key) || strings.TrimSpace(value) == "" {
			continue
		}
		encrypted, err := s.encryptSecretBytes([]byte(value))
		if err != nil {
			return "", err
		}
		headers[key] = string(encrypted)
	}
	data, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Store) decryptHeadersJSON(headersJSON string) (string, error) {
	headersJSON = strings.TrimSpace(headersJSON)
	if headersJSON == "" {
		return "{}", nil
	}
	headers := map[string]string{}
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		return "", err
	}
	for key, value := range headers {
		if !isSecretChannelHeader(key) || !strings.HasPrefix(value, secretEnvelopeV1) {
			continue
		}
		plaintext, err := s.decryptSecretBytes([]byte(value))
		if err != nil {
			return "", err
		}
		headers[key] = string(plaintext)
	}
	data, err := json.Marshal(headers)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func isSecretChannelHeader(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "authorization" || strings.Contains(key, "api-key") || strings.Contains(key, "apikey") || strings.Contains(key, "token")
}

func sqliteDSN(dbPath string) string {
	dbPath = normalizeSQLiteFilePath(dbPath)
	values := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"busy_timeout(5000)",
		"wal_autocheckpoint(1000)",
	} {
		values.Add("_pragma", pragma)
	}
	u := url.URL{
		Scheme:   "file",
		Path:     dbPath,
		RawQuery: values.Encode(),
	}
	return u.String()
}

func normalizeSQLiteFilePath(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" || dbPath == ":memory:" || filepath.IsAbs(dbPath) {
		return dbPath
	}
	if abs, err := filepath.Abs(dbPath); err == nil {
		return abs
	}
	return dbPath
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	if s.client != nil {
		return s.client.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) initSchema() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS logs (
			path TEXT PRIMARY KEY,
			trace_id TEXT NOT NULL DEFAULT '',
			mod_time_ns INTEGER NOT NULL,
			file_size INTEGER NOT NULL,
			version TEXT NOT NULL,
			request_id TEXT NOT NULL,
			recorded_at datetime NOT NULL,
			model TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT '',
			operation TEXT NOT NULL DEFAULT '',
			endpoint TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL,
			method TEXT NOT NULL,
			status_code INTEGER NOT NULL,
			duration_ms INTEGER NOT NULL,
			ttft_ms INTEGER NOT NULL,
			client_ip TEXT NOT NULL,
			content_length INTEGER NOT NULL,
			error_text TEXT NOT NULL,
			prompt_tokens INTEGER NOT NULL,
			completion_tokens INTEGER NOT NULL,
			total_tokens INTEGER NOT NULL,
			cached_tokens INTEGER NOT NULL,
			req_header_len INTEGER NOT NULL,
			req_body_len INTEGER NOT NULL,
			res_header_len INTEGER NOT NULL,
			res_body_len INTEGER NOT NULL,
			is_stream bool NOT NULL DEFAULT false,
			session_id TEXT NOT NULL DEFAULT '',
			session_source TEXT NOT NULL DEFAULT '',
			window_id TEXT NOT NULL DEFAULT '',
			client_request_id TEXT NOT NULL DEFAULT '',
			selected_upstream_id TEXT NOT NULL DEFAULT '',
			selected_upstream_base_url TEXT NOT NULL DEFAULT '',
			selected_upstream_provider_preset TEXT NOT NULL DEFAULT '',
			routing_policy TEXT NOT NULL DEFAULT '',
			routing_score REAL NOT NULL DEFAULT 0,
			routing_candidate_count INTEGER NOT NULL DEFAULT 0,
			routing_failure_reason TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS upstream_targets (
			id TEXT PRIMARY KEY,
			base_url TEXT NOT NULL,
			provider_preset TEXT NOT NULL,
			protocol_family TEXT NOT NULL,
			routing_profile TEXT NOT NULL,
			enabled bool NOT NULL DEFAULT true,
			priority INTEGER NOT NULL,
			weight REAL NOT NULL,
			capacity_hint REAL NOT NULL,
			last_refresh_at datetime NULL,
			last_refresh_status TEXT NOT NULL DEFAULT '',
			last_refresh_error TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS upstream_models (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			upstream_id TEXT NOT NULL,
			model TEXT NOT NULL,
			source TEXT NOT NULL,
			seen_at datetime NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS upstreammodel_upstream_id_model ON upstream_models(upstream_id, model);`,
		`CREATE INDEX IF NOT EXISTS idx_upstream_models_model ON upstream_models(model);`,
		`CREATE TABLE IF NOT EXISTS channel_configs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			base_url TEXT NOT NULL,
			provider_preset TEXT NOT NULL DEFAULT '',
			protocol_family TEXT NOT NULL DEFAULT '',
			routing_profile TEXT NOT NULL DEFAULT '',
			api_version TEXT NOT NULL DEFAULT '',
			deployment TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			location TEXT NOT NULL DEFAULT '',
			model_resource TEXT NOT NULL DEFAULT '',
			api_key_ciphertext BLOB NULL,
			api_key_hint TEXT NOT NULL DEFAULT '',
			headers_json TEXT NOT NULL DEFAULT '{}',
			enabled bool NOT NULL DEFAULT true,
			priority INTEGER NOT NULL DEFAULT 0,
			weight REAL NOT NULL DEFAULT 1,
			capacity_hint REAL NOT NULL DEFAULT 1,
			model_discovery TEXT NOT NULL DEFAULT 'list_models',
			allow_unknown_models bool NOT NULL DEFAULT false,
			created_at datetime NOT NULL,
			updated_at datetime NOT NULL,
			last_probe_at datetime NULL,
			last_probe_status TEXT NOT NULL DEFAULT '',
			last_probe_error TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS channelconfig_enabled_priority ON channel_configs(enabled, priority);`,
		`CREATE INDEX IF NOT EXISTS channelconfig_provider_preset ON channel_configs(provider_preset);`,
		`CREATE TABLE IF NOT EXISTS channel_models (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			channel_id TEXT NOT NULL,
			model TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			enabled bool NOT NULL DEFAULT true,
			supports_responses INTEGER NULL,
			supports_chat_completions INTEGER NULL,
			supports_embeddings INTEGER NULL,
			context_window INTEGER NULL,
			input_modalities_json TEXT NOT NULL DEFAULT '[]',
			output_modalities_json TEXT NOT NULL DEFAULT '[]',
			raw_model_json TEXT NOT NULL DEFAULT '{}',
			first_seen_at datetime NOT NULL,
			last_seen_at datetime NOT NULL,
			last_probe_at datetime NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS channelmodel_channel_id_model ON channel_models(channel_id, model);`,
		`CREATE INDEX IF NOT EXISTS idx_channel_models_model ON channel_models(model);`,
		`CREATE INDEX IF NOT EXISTS channelmodel_channel_id_enabled ON channel_models(channel_id, enabled);`,
		`CREATE TABLE IF NOT EXISTS model_catalog (
			model TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT '',
			family TEXT NOT NULL DEFAULT '',
			vendor TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]',
			first_seen_at datetime NOT NULL,
			last_seen_at datetime NOT NULL,
			last_used_at datetime NULL
		);`,
		`CREATE TABLE IF NOT EXISTS channel_probe_runs (
			id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at datetime NOT NULL,
			completed_at datetime NULL,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			discovered_count INTEGER NOT NULL DEFAULT 0,
			enabled_count INTEGER NOT NULL DEFAULT 0,
			endpoint TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0,
			error_text TEXT NOT NULL DEFAULT '',
			request_meta_json TEXT NOT NULL DEFAULT '{}',
			response_sample_json TEXT NOT NULL DEFAULT '{}'
		);`,
		`CREATE INDEX IF NOT EXISTS channelproberun_channel_id_started_at ON channel_probe_runs(channel_id, started_at);`,
		`CREATE INDEX IF NOT EXISTS channelproberun_status_started_at ON channel_probe_runs(status, started_at);`,
		`CREATE TABLE IF NOT EXISTS datasets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at datetime NOT NULL,
			updated_at datetime NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS dataset_examples (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			dataset_id TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			added_at datetime NOT NULL,
			source_type TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS datasetexample_dataset_id_trace_id ON dataset_examples(dataset_id, trace_id);`,
		`CREATE INDEX IF NOT EXISTS idx_dataset_examples_dataset_position ON dataset_examples(dataset_id, position ASC);`,
		`CREATE TABLE IF NOT EXISTS eval_runs (
			id TEXT PRIMARY KEY,
			dataset_id TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			evaluator_set TEXT NOT NULL,
			created_at datetime NOT NULL,
			completed_at datetime NOT NULL,
			trace_count INTEGER NOT NULL DEFAULT 0,
			score_count INTEGER NOT NULL DEFAULT 0,
			pass_count INTEGER NOT NULL DEFAULT 0,
			fail_count INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS scores (
			id TEXT PRIMARY KEY,
			trace_id TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			dataset_id TEXT NOT NULL DEFAULT '',
			eval_run_id TEXT NOT NULL DEFAULT '',
			evaluator_key TEXT NOT NULL,
			value REAL NOT NULL,
			status TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			explanation TEXT NOT NULL DEFAULT '',
			created_at datetime NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_scores_trace_id ON scores(trace_id, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_scores_session_id ON scores(session_id, created_at DESC) WHERE session_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_scores_dataset_id ON scores(dataset_id, created_at DESC) WHERE dataset_id <> '';`,
		`CREATE INDEX IF NOT EXISTS idx_scores_eval_run_id ON scores(eval_run_id, created_at DESC) WHERE eval_run_id <> '';`,
		`CREATE TABLE IF NOT EXISTS experiment_runs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			baseline_eval_run_id TEXT NOT NULL,
			candidate_eval_run_id TEXT NOT NULL,
			created_at datetime NOT NULL,
			baseline_score_count INTEGER NOT NULL DEFAULT 0,
			candidate_score_count INTEGER NOT NULL DEFAULT 0,
			baseline_pass_rate REAL NOT NULL DEFAULT 0,
			candidate_pass_rate REAL NOT NULL DEFAULT 0,
			pass_rate_delta REAL NOT NULL DEFAULT 0,
			matched_score_count INTEGER NOT NULL DEFAULT 0,
			improvement_count INTEGER NOT NULL DEFAULT 0,
			regression_count INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_experiment_runs_created_at ON experiment_runs(created_at DESC, id DESC);`,
		`CREATE TABLE IF NOT EXISTS trace_observations (
			trace_id TEXT PRIMARY KEY,
			parser TEXT NOT NULL,
			parser_version TEXT NOT NULL,
			status TEXT NOT NULL,
			provider TEXT NOT NULL DEFAULT '',
			operation TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			summary_json TEXT NOT NULL DEFAULT '{}',
			warnings_json TEXT NOT NULL DEFAULT '[]',
			created_at datetime NOT NULL,
			updated_at datetime NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_trace_observations_status ON trace_observations(status, updated_at DESC);`,
		`CREATE TABLE IF NOT EXISTS semantic_nodes (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			trace_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			parent_node_id TEXT NOT NULL DEFAULT '',
			provider_type TEXT NOT NULL DEFAULT '',
			normalized_type TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			node_index INTEGER NOT NULL DEFAULT 0,
			depth INTEGER NOT NULL DEFAULT 0,
			text_preview TEXT NOT NULL DEFAULT '',
			json TEXT NOT NULL DEFAULT '',
			raw TEXT NOT NULL DEFAULT '',
			raw_ref TEXT NOT NULL DEFAULT '',
			created_at datetime NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS semantic_nodes_trace_node_key ON semantic_nodes(trace_id, node_id);`,
		`CREATE INDEX IF NOT EXISTS idx_semantic_nodes_trace_depth ON semantic_nodes(trace_id, depth, node_index);`,
		`CREATE TABLE IF NOT EXISTS trace_findings (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			trace_id TEXT NOT NULL,
			finding_id TEXT NOT NULL,
			category TEXT NOT NULL,
			severity TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			evidence_path TEXT NOT NULL DEFAULT '',
			evidence_excerpt TEXT NOT NULL DEFAULT '',
			node_id TEXT NOT NULL DEFAULT '',
			detector TEXT NOT NULL,
			detector_version TEXT NOT NULL,
			created_at datetime NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS trace_findings_trace_finding_key ON trace_findings(trace_id, finding_id);`,
		`CREATE INDEX IF NOT EXISTS idx_trace_findings_trace_severity ON trace_findings(trace_id, severity, category);`,
		`CREATE TABLE IF NOT EXISTS analysis_runs (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			trace_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			analyzer TEXT NOT NULL,
			analyzer_version TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			input_ref TEXT NOT NULL DEFAULT '',
			output_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL,
			created_at datetime NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_analysis_runs_session_kind ON analysis_runs(session_id, kind, created_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_analysis_runs_trace_kind ON analysis_runs(trace_id, kind, created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS parse_jobs (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			trace_id TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at datetime NOT NULL,
			updated_at datetime NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_parse_jobs_status ON parse_jobs(status, updated_at ASC);`,
		`CREATE TABLE IF NOT EXISTS parser_versions (
			parser TEXT NOT NULL,
			version TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at datetime NOT NULL,
			PRIMARY KEY(parser, version)
		);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColumn("logs", "trace_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "provider", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "operation", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "endpoint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "session_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "session_source", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "window_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "client_request_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "selected_upstream_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "selected_upstream_base_url", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "selected_upstream_provider_preset", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_policy", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_score", "REAL NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_candidate_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("logs", "routing_failure_reason", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.backfillTraceIDs(); err != nil {
		return err
	}
	if err := s.ensureLogsDatetimeTable(); err != nil {
		return err
	}
	postColumnStmts := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS logs_trace_id_key ON logs(trace_id);`,
		`CREATE INDEX IF NOT EXISTS tracelog_recorded_at ON logs(recorded_at);`,
		`CREATE INDEX IF NOT EXISTS tracelog_model_recorded_at ON logs(model, recorded_at);`,
		`CREATE INDEX IF NOT EXISTS tracelog_session_id_recorded_at ON logs(session_id, recorded_at);`,
		`CREATE INDEX IF NOT EXISTS tracelog_request_id ON logs(request_id);`,
	}
	for _, stmt := range postColumnStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.ensureEntCompatibleTables(); err != nil {
		return err
	}
	if err := s.backfillSemantics(); err != nil {
		return err
	}
	if err := s.backfillGrouping(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureEntCompatibleTables() error {
	if err := s.ensureUpstreamTargetsEntTable(); err != nil {
		return err
	}
	if err := s.ensureAutoIDTable(
		"upstream_models",
		`CREATE TABLE upstream_models (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			upstream_id TEXT NOT NULL,
			model TEXT NOT NULL,
			source TEXT NOT NULL,
			seen_at datetime NOT NULL
		)`,
		`INSERT INTO upstream_models (upstream_id, model, source, seen_at)
		 SELECT upstream_id, model, source, seen_at FROM upstream_models_old`,
		[]string{
			`CREATE UNIQUE INDEX IF NOT EXISTS upstreammodel_upstream_id_model ON upstream_models(upstream_id, model)`,
			`CREATE INDEX IF NOT EXISTS idx_upstream_models_model ON upstream_models(model)`,
		},
	); err != nil {
		return err
	}
	return s.ensureAutoIDTable(
		"dataset_examples",
		`CREATE TABLE dataset_examples (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			dataset_id TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			added_at TEXT NOT NULL,
			source_type TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO dataset_examples (dataset_id, trace_id, position, added_at, source_type, source_id, note)
		 SELECT dataset_id, trace_id, position, added_at, source_type, source_id, note FROM dataset_examples_old`,
		[]string{
			`CREATE UNIQUE INDEX IF NOT EXISTS datasetexample_dataset_id_trace_id ON dataset_examples(dataset_id, trace_id)`,
			`CREATE INDEX IF NOT EXISTS idx_dataset_examples_dataset_position ON dataset_examples(dataset_id, position ASC)`,
		},
	)
}

func (s *Store) ensureUpstreamTargetsEntTable() error {
	enabledType, err := s.columnType("upstream_targets", "enabled")
	if err != nil {
		return err
	}
	lastRefreshAtType, err := s.columnType("upstream_targets", "last_refresh_at")
	if err != nil {
		return err
	}
	if strings.EqualFold(enabledType, "bool") && strings.EqualFold(lastRefreshAtType, "datetime") {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE upstream_targets RENAME TO upstream_targets_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE upstream_targets (
		id TEXT PRIMARY KEY,
		base_url TEXT NOT NULL DEFAULT '',
		provider_preset TEXT NOT NULL DEFAULT '',
		protocol_family TEXT NOT NULL DEFAULT '',
		routing_profile TEXT NOT NULL DEFAULT '',
		enabled bool NOT NULL DEFAULT true,
		priority INTEGER NOT NULL DEFAULT 0,
		weight REAL NOT NULL DEFAULT 0,
		capacity_hint REAL NOT NULL DEFAULT 0,
		last_refresh_at datetime NULL,
		last_refresh_status TEXT NOT NULL DEFAULT '',
		last_refresh_error TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO upstream_targets (
		id, base_url, provider_preset, protocol_family, routing_profile, enabled,
		priority, weight, capacity_hint, last_refresh_at, last_refresh_status, last_refresh_error
	)
	SELECT
		id, base_url, provider_preset, protocol_family, routing_profile,
		CASE WHEN enabled IN (1, '1', 'true', 'TRUE') THEN true ELSE false END,
		priority, weight, capacity_hint,
		CASE WHEN last_refresh_at IS NULL OR TRIM(CAST(last_refresh_at AS text)) = '' THEN NULL ELSE last_refresh_at END,
		last_refresh_status, last_refresh_error
	FROM upstream_targets_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE upstream_targets_old`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ensureLogsDatetimeTable() error {
	recordedAtType, err := s.columnType("logs", "recorded_at")
	if err != nil {
		return err
	}
	isStreamType, err := s.columnType("logs", "is_stream")
	if err != nil {
		return err
	}
	if strings.EqualFold(recordedAtType, "datetime") && strings.EqualFold(isStreamType, "bool") {
		return nil
	}

	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_logs_recorded_at`,
		`DROP INDEX IF EXISTS idx_logs_model_recorded_at`,
		`DROP INDEX IF EXISTS idx_logs_trace_id`,
		`DROP INDEX IF EXISTS idx_logs_session_id_recorded_at`,
		`DROP INDEX IF EXISTS logs_trace_id_key`,
		`DROP INDEX IF EXISTS tracelog_recorded_at`,
		`DROP INDEX IF EXISTS tracelog_model_recorded_at`,
		`DROP INDEX IF EXISTS tracelog_session_id_recorded_at`,
		`DROP INDEX IF EXISTS tracelog_request_id`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE logs RENAME TO logs_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE logs (
		path TEXT PRIMARY KEY,
		trace_id TEXT NOT NULL DEFAULT '',
		mod_time_ns INTEGER NOT NULL,
		file_size INTEGER NOT NULL,
		version TEXT NOT NULL,
		request_id TEXT NOT NULL DEFAULT '',
		recorded_at datetime NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		operation TEXT NOT NULL DEFAULT '',
		endpoint TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL DEFAULT '',
		method TEXT NOT NULL DEFAULT '',
		status_code INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		ttft_ms INTEGER NOT NULL DEFAULT 0,
		client_ip TEXT NOT NULL DEFAULT '',
		content_length INTEGER NOT NULL DEFAULT 0,
		error_text TEXT NOT NULL DEFAULT '',
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		req_header_len INTEGER NOT NULL DEFAULT 0,
		req_body_len INTEGER NOT NULL DEFAULT 0,
		res_header_len INTEGER NOT NULL DEFAULT 0,
		res_body_len INTEGER NOT NULL DEFAULT 0,
		is_stream bool NOT NULL DEFAULT false,
		session_id TEXT NOT NULL DEFAULT '',
		session_source TEXT NOT NULL DEFAULT '',
		window_id TEXT NOT NULL DEFAULT '',
		client_request_id TEXT NOT NULL DEFAULT '',
		selected_upstream_id TEXT NOT NULL DEFAULT '',
		selected_upstream_base_url TEXT NOT NULL DEFAULT '',
		selected_upstream_provider_preset TEXT NOT NULL DEFAULT '',
		routing_policy TEXT NOT NULL DEFAULT '',
		routing_score REAL NOT NULL DEFAULT 0,
		routing_candidate_count INTEGER NOT NULL DEFAULT 0,
		routing_failure_reason TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO logs (
		path, trace_id, mod_time_ns, file_size, version, request_id, recorded_at, model, provider, operation, endpoint, url, method,
		status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
		prompt_tokens, completion_tokens, total_tokens, cached_tokens,
		req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
		session_id, session_source, window_id, client_request_id,
		selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
		routing_policy, routing_score, routing_candidate_count, routing_failure_reason
	)
	SELECT
		path, trace_id, mod_time_ns, file_size, version, request_id,
		CASE WHEN recorded_at IS NULL OR TRIM(CAST(recorded_at AS text)) = '' THEN '1970-01-01T00:00:00Z' ELSE recorded_at END,
		model, provider, operation, endpoint, url, method,
		status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
		prompt_tokens, completion_tokens, total_tokens, cached_tokens,
		req_header_len, req_body_len, res_header_len, res_body_len,
		CASE WHEN is_stream IN (1, '1', 'true', 'TRUE') THEN true ELSE false END,
		session_id, session_source, window_id, client_request_id,
		selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
		routing_policy, routing_score, routing_candidate_count, routing_failure_reason
	FROM logs_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE logs_old`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ensureAutoIDTable(table string, createSQL string, copySQL string, indexes []string) error {
	hasID, err := s.hasColumn(table, "id")
	if err != nil {
		return err
	}
	if hasID {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE ` + table + ` RENAME TO ` + table + `_old`); err != nil {
		return err
	}
	if _, err := tx.Exec(createSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(copySQL); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE ` + table + `_old`); err != nil {
		return err
	}
	for _, stmt := range indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ensureColumn(table string, column string, definition string) error {
	exists, err := s.hasColumn(table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

func (s *Store) hasColumn(table string, column string) (bool, error) {
	typ, err := s.columnType(table, column)
	if err != nil {
		return false, err
	}
	return typ != "", nil
}

func (s *Store) columnType(table string, column string) (string, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var (
		cid        int
		name       string
		typ        string
		notNull    int
		defaultVal sql.NullString
		pk         int
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			return "", err
		}
		if name == column {
			return typ, rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func (s *Store) backfillTraceIDs() error {
	rows, err := s.db.Query(`SELECT path FROM logs WHERE trace_id = '' OR trace_id IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, path := range paths {
		if _, err := s.db.Exec(`UPDATE logs SET trace_id = ? WHERE path = ?`, uuid.NewString(), path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backfillSemantics() error {
	rows, err := s.db.Query(`SELECT path, url, provider, operation, endpoint FROM logs`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rowData struct {
		path      string
		url       string
		provider  string
		operation string
		endpoint  string
	}
	var updates []rowData
	for rows.Next() {
		var row rowData
		if err := rows.Scan(&row.path, &row.url, &row.provider, &row.operation, &row.endpoint); err != nil {
			return err
		}
		if row.provider != "" && row.operation != "" && row.endpoint != "" {
			continue
		}
		semantics := llm.ClassifyPath(row.url, "")
		row.provider = semantics.Provider
		row.operation = semantics.Operation
		row.endpoint = semantics.Endpoint
		updates = append(updates, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, update := range updates {
		if _, err := s.db.Exec(
			`UPDATE logs SET provider = ?, operation = ?, endpoint = ? WHERE path = ?`,
			update.provider,
			update.operation,
			update.endpoint,
			update.path,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertLog(path string, header recordfile.RecordHeader) error {
	return s.UpsertLogWithGrouping(path, header, GroupingInfo{})
}

func (s *Store) UpsertUpstreamTarget(record UpstreamTargetRecord) error {
	create := s.client.UpstreamTarget.Create().
		SetID(record.ID).
		SetBaseURL(record.BaseURL).
		SetProviderPreset(record.ProviderPreset).
		SetProtocolFamily(record.ProtocolFamily).
		SetRoutingProfile(record.RoutingProfile).
		SetEnabled(record.Enabled).
		SetPriority(record.Priority).
		SetWeight(record.Weight).
		SetCapacityHint(record.CapacityHint).
		SetLastRefreshStatus(record.LastRefreshStatus).
		SetLastRefreshError(record.LastRefreshError)
	if !record.LastRefreshAt.IsZero() {
		create.SetLastRefreshAt(record.LastRefreshAt.UTC())
	}
	return create.
		OnConflictColumns(upstreamtarget.FieldID).
		UpdateNewValues().
		Exec(context.Background())
}

func (s *Store) CreateDataset(name string, description string) (DatasetRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return DatasetRecord{}, fmt.Errorf("dataset name is required")
	}
	now := time.Now().UTC()
	record := DatasetRecord{
		ID:          uuid.NewString(),
		Name:        name,
		Description: strings.TrimSpace(description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.client.Dataset.Create().
		SetID(record.ID).
		SetName(record.Name).
		SetDescription(record.Description).
		SetCreatedAt(record.CreatedAt).
		SetUpdatedAt(record.UpdatedAt).
		Exec(context.Background()); err != nil {
		return DatasetRecord{}, err
	}
	return record, nil
}

func (s *Store) ListDatasets() ([]DatasetRecord, error) {
	ctx := context.Background()
	rows, err := s.client.Dataset.Query().
		Order(dataset.ByUpdatedAt(entsql.OrderDesc()), dataset.ByID(entsql.OrderDesc())).
		All(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]DatasetRecord, 0, len(rows))
	for _, row := range rows {
		count, err := s.client.DatasetExample.Query().
			Where(datasetexample.DatasetIDEQ(row.ID)).
			Count(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, datasetRecordFromEnt(row, count))
	}
	return out, nil
}

func (s *Store) GetDataset(datasetID string) (DatasetRecord, error) {
	ctx := context.Background()
	row, err := s.client.Dataset.Get(ctx, datasetID)
	if err != nil {
		if dao.IsNotFound(err) {
			return DatasetRecord{}, sql.ErrNoRows
		}
		return DatasetRecord{}, err
	}
	count, err := s.client.DatasetExample.Query().
		Where(datasetexample.DatasetIDEQ(row.ID)).
		Count(ctx)
	if err != nil {
		return DatasetRecord{}, err
	}
	return datasetRecordFromEnt(row, count), nil
}

func (s *Store) AppendDatasetExamples(datasetID string, traceIDs []string, sourceType string, sourceID string, note string) (int, int, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return 0, 0, fmt.Errorf("dataset id is required")
	}
	if _, err := s.GetDataset(datasetID); err != nil {
		return 0, 0, err
	}

	seen := map[string]struct{}{}
	ordered := make([]string, 0, len(traceIDs))
	for _, traceID := range traceIDs {
		traceID = strings.TrimSpace(traceID)
		if traceID == "" {
			continue
		}
		if _, ok := seen[traceID]; ok {
			continue
		}
		seen[traceID] = struct{}{}
		ordered = append(ordered, traceID)
	}
	if len(ordered) == 0 {
		return 0, 0, nil
	}

	ctx := context.Background()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	nextPosition := 0
	existingCount, err := tx.DatasetExample.Query().
		Where(datasetexample.DatasetIDEQ(datasetID)).
		Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	if existingCount > 0 {
		nextPosition, err = tx.DatasetExample.Query().
			Where(datasetexample.DatasetIDEQ(datasetID)).
			Aggregate(dao.Max(datasetexample.FieldPosition)).
			Int(ctx)
		if err != nil {
			return 0, 0, err
		}
	}
	now := time.Now().UTC()
	added := 0
	skipped := 0
	creates := make([]*dao.DatasetExampleCreate, 0, len(ordered))
	for _, traceID := range ordered {
		if _, err := s.GetByID(traceID); err != nil {
			return 0, 0, err
		}
		exists, err := tx.DatasetExample.Query().
			Where(datasetexample.DatasetIDEQ(datasetID), datasetexample.TraceIDEQ(traceID)).
			Count(ctx)
		if err != nil {
			return 0, 0, err
		}
		if exists > 0 {
			skipped++
			continue
		}
		nextPosition++
		added++
		creates = append(creates, tx.DatasetExample.Create().
			SetDatasetID(datasetID).
			SetTraceID(traceID).
			SetPosition(nextPosition).
			SetAddedAt(now).
			SetSourceType(strings.TrimSpace(sourceType)).
			SetSourceID(strings.TrimSpace(sourceID)).
			SetNote(strings.TrimSpace(note)))
	}
	if len(creates) > 0 {
		if err := tx.DatasetExample.CreateBulk(creates...).
			OnConflictColumns(datasetexample.FieldDatasetID, datasetexample.FieldTraceID).
			DoNothing().
			Exec(ctx); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Dataset.UpdateOneID(datasetID).SetUpdatedAt(now).Exec(ctx); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return added, skipped, nil
}

func (s *Store) GetDatasetExamples(datasetID string) ([]DatasetExampleRecord, error) {
	rows, err := s.client.DatasetExample.Query().
		Where(datasetexample.DatasetIDEQ(datasetID)).
		Order(datasetexample.ByPosition(), datasetexample.ByTraceID()).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	out := make([]DatasetExampleRecord, 0, len(rows))
	for _, row := range rows {
		trace, err := s.GetByID(row.TraceID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, DatasetExampleRecord{
			DatasetID:  row.DatasetID,
			TraceID:    row.TraceID,
			Position:   row.Position,
			AddedAt:    row.AddedAt,
			SourceType: row.SourceType,
			SourceID:   row.SourceID,
			Note:       row.Note,
			Trace:      trace,
		})
	}
	return out, nil
}

func (s *Store) CreateEvalRun(datasetID string, sourceType string, sourceID string, evaluatorSet string, traceCount int) (EvalRunRecord, error) {
	evaluatorSet = strings.TrimSpace(evaluatorSet)
	if evaluatorSet == "" {
		return EvalRunRecord{}, fmt.Errorf("evaluator set is required")
	}
	now := time.Now().UTC()
	record := EvalRunRecord{
		ID:           uuid.NewString(),
		DatasetID:    strings.TrimSpace(datasetID),
		SourceType:   strings.TrimSpace(sourceType),
		SourceID:     strings.TrimSpace(sourceID),
		EvaluatorSet: evaluatorSet,
		CreatedAt:    now,
		CompletedAt:  now,
		TraceCount:   traceCount,
	}
	if err := s.client.EvalRun.Create().
		SetID(record.ID).
		SetDatasetID(record.DatasetID).
		SetSourceType(record.SourceType).
		SetSourceID(record.SourceID).
		SetEvaluatorSet(record.EvaluatorSet).
		SetCreatedAt(record.CreatedAt).
		SetCompletedAt(record.CompletedAt).
		SetTraceCount(record.TraceCount).
		SetScoreCount(0).
		SetPassCount(0).
		SetFailCount(0).
		Exec(context.Background()); err != nil {
		return EvalRunRecord{}, err
	}
	return record, nil
}

func (s *Store) FinalizeEvalRun(evalRunID string, scoreCount int, passCount int, failCount int) error {
	_, err := s.client.EvalRun.Update().
		Where(evalrun.IDEQ(evalRunID)).
		SetCompletedAt(time.Now().UTC()).
		SetScoreCount(scoreCount).
		SetPassCount(passCount).
		SetFailCount(failCount).
		Save(context.Background())
	return err
}

func (s *Store) AddScore(record ScoreRecord) (ScoreRecord, error) {
	if strings.TrimSpace(record.TraceID) == "" {
		return ScoreRecord{}, fmt.Errorf("trace id is required")
	}
	if strings.TrimSpace(record.EvaluatorKey) == "" {
		return ScoreRecord{}, fmt.Errorf("evaluator key is required")
	}
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	record.CreatedAt = now
	if err := s.client.Score.Create().
		SetID(record.ID).
		SetTraceID(record.TraceID).
		SetSessionID(record.SessionID).
		SetDatasetID(record.DatasetID).
		SetEvalRunID(record.EvalRunID).
		SetEvaluatorKey(record.EvaluatorKey).
		SetValue(record.Value).
		SetStatus(record.Status).
		SetLabel(record.Label).
		SetExplanation(record.Explanation).
		SetCreatedAt(record.CreatedAt).
		Exec(context.Background()); err != nil {
		return ScoreRecord{}, err
	}
	return record, nil
}

func (s *Store) GetEvalRun(evalRunID string) (EvalRunRecord, error) {
	row, err := s.client.EvalRun.Get(context.Background(), evalRunID)
	if err != nil {
		if dao.IsNotFound(err) {
			return EvalRunRecord{}, sql.ErrNoRows
		}
		return EvalRunRecord{}, err
	}
	return evalRunRecordFromEnt(row), nil
}

func (s *Store) ListEvalRuns(limit int) ([]EvalRunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.client.EvalRun.Query().
		Order(evalrun.ByCreatedAt(entsql.OrderDesc()), evalrun.ByID(entsql.OrderDesc())).
		Limit(limit).
		All(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]EvalRunRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, evalRunRecordFromEnt(row))
	}
	return out, nil
}

func (s *Store) ListScores(filter ScoreFilter, limit int) ([]ScoreRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	var predicates []predicate.Score
	if traceID := strings.TrimSpace(filter.TraceID); traceID != "" {
		predicates = append(predicates, score.TraceIDEQ(traceID))
	}
	if sessionID := strings.TrimSpace(filter.SessionID); sessionID != "" {
		predicates = append(predicates, score.SessionIDEQ(sessionID))
	}
	if datasetID := strings.TrimSpace(filter.DatasetID); datasetID != "" {
		predicates = append(predicates, score.DatasetIDEQ(datasetID))
	}
	if evalRunID := strings.TrimSpace(filter.EvalRunID); evalRunID != "" {
		predicates = append(predicates, score.EvalRunIDEQ(evalRunID))
	}
	rows, err := s.client.Score.Query().
		Where(predicates...).
		Order(score.ByCreatedAt(entsql.OrderDesc()), score.ByID(entsql.OrderDesc())).
		Limit(limit).
		All(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]ScoreRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, scoreRecordFromEnt(row))
	}
	return out, nil
}

func (s *Store) CreateExperimentRun(record ExperimentRunRecord) (ExperimentRunRecord, error) {
	if strings.TrimSpace(record.BaselineEvalRunID) == "" {
		return ExperimentRunRecord{}, fmt.Errorf("baseline eval run id is required")
	}
	if strings.TrimSpace(record.CandidateEvalRunID) == "" {
		return ExperimentRunRecord{}, fmt.Errorf("candidate eval run id is required")
	}
	record.ID = uuid.NewString()
	record.CreatedAt = time.Now().UTC()
	record.Name = strings.TrimSpace(record.Name)
	record.Description = strings.TrimSpace(record.Description)
	record.BaselineEvalRunID = strings.TrimSpace(record.BaselineEvalRunID)
	record.CandidateEvalRunID = strings.TrimSpace(record.CandidateEvalRunID)
	if err := s.client.ExperimentRun.Create().
		SetID(record.ID).
		SetName(record.Name).
		SetDescription(record.Description).
		SetBaselineEvalRunID(record.BaselineEvalRunID).
		SetCandidateEvalRunID(record.CandidateEvalRunID).
		SetCreatedAt(record.CreatedAt).
		SetBaselineScoreCount(record.BaselineScoreCount).
		SetCandidateScoreCount(record.CandidateScoreCount).
		SetBaselinePassRate(record.BaselinePassRate).
		SetCandidatePassRate(record.CandidatePassRate).
		SetPassRateDelta(record.PassRateDelta).
		SetMatchedScoreCount(record.MatchedScoreCount).
		SetImprovementCount(record.ImprovementCount).
		SetRegressionCount(record.RegressionCount).
		Exec(context.Background()); err != nil {
		return ExperimentRunRecord{}, err
	}
	return record, nil
}

func (s *Store) GetExperimentRun(experimentRunID string) (ExperimentRunRecord, error) {
	row, err := s.client.ExperimentRun.Get(context.Background(), experimentRunID)
	if err != nil {
		if dao.IsNotFound(err) {
			return ExperimentRunRecord{}, sql.ErrNoRows
		}
		return ExperimentRunRecord{}, err
	}
	return experimentRunRecordFromEnt(row), nil
}

func (s *Store) ListExperimentRuns(limit int) ([]ExperimentRunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.client.ExperimentRun.Query().
		Order(experimentrun.ByCreatedAt(entsql.OrderDesc()), experimentrun.ByID(entsql.OrderDesc())).
		Limit(limit).
		All(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]ExperimentRunRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, experimentRunRecordFromEnt(row))
	}
	return out, nil
}

func (s *Store) ReplaceUpstreamModels(upstreamID string, records []UpstreamModelRecord) error {
	ctx := context.Background()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.UpstreamModel.Delete().Where(upstreammodel.UpstreamIDEQ(upstreamID)).Exec(ctx); err != nil {
		return err
	}
	creates := make([]*dao.UpstreamModelCreate, 0, len(records))
	for _, record := range records {
		seenAt := record.SeenAt
		if seenAt.IsZero() {
			seenAt = time.Now().UTC()
		}
		creates = append(creates, tx.UpstreamModel.Create().
			SetUpstreamID(upstreamID).
			SetModel(record.Model).
			SetSource(record.Source).
			SetSeenAt(seenAt.UTC()))
	}
	if len(creates) > 0 {
		if err := tx.UpstreamModel.CreateBulk(creates...).
			OnConflictColumns(upstreammodel.FieldUpstreamID, upstreammodel.FieldModel).
			UpdateNewValues().
			Exec(ctx); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) UpsertLogWithGrouping(path string, header recordfile.RecordHeader, grouping GroupingInfo) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	traceID, err := s.lookupOrCreateTraceID(path)
	if err != nil {
		return err
	}

	cachedTokens := 0
	if header.Usage.PromptTokenDetails != nil {
		cachedTokens = header.Usage.PromptTokenDetails.CachedTokens
	}
	if header.Meta.Provider == "" || header.Meta.Operation == "" || header.Meta.Endpoint == "" {
		semantics := llm.ClassifyPath(header.Meta.URL, "")
		if header.Meta.Provider == "" {
			header.Meta.Provider = semantics.Provider
		}
		if header.Meta.Operation == "" {
			header.Meta.Operation = semantics.Operation
		}
		if header.Meta.Endpoint == "" {
			header.Meta.Endpoint = semantics.Endpoint
		}
	}

	_, err = s.db.Exec(`
		INSERT INTO logs (
			path, trace_id, mod_time_ns, file_size, version, request_id, recorded_at, model, provider, operation, endpoint, url, method,
			status_code, duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			trace_id=CASE WHEN logs.trace_id = '' THEN excluded.trace_id ELSE logs.trace_id END,
			mod_time_ns=excluded.mod_time_ns,
			file_size=excluded.file_size,
			version=excluded.version,
			request_id=excluded.request_id,
			recorded_at=excluded.recorded_at,
			model=excluded.model,
			provider=excluded.provider,
			operation=excluded.operation,
			endpoint=excluded.endpoint,
			url=excluded.url,
			method=excluded.method,
			status_code=excluded.status_code,
			duration_ms=excluded.duration_ms,
			ttft_ms=excluded.ttft_ms,
			client_ip=excluded.client_ip,
			content_length=excluded.content_length,
			error_text=excluded.error_text,
			prompt_tokens=excluded.prompt_tokens,
			completion_tokens=excluded.completion_tokens,
			total_tokens=excluded.total_tokens,
			cached_tokens=excluded.cached_tokens,
			req_header_len=excluded.req_header_len,
			req_body_len=excluded.req_body_len,
			res_header_len=excluded.res_header_len,
			res_body_len=excluded.res_body_len,
			is_stream=excluded.is_stream,
			session_id=excluded.session_id,
			session_source=excluded.session_source,
			window_id=excluded.window_id,
			client_request_id=excluded.client_request_id,
			selected_upstream_id=excluded.selected_upstream_id,
			selected_upstream_base_url=excluded.selected_upstream_base_url,
			selected_upstream_provider_preset=excluded.selected_upstream_provider_preset,
			routing_policy=excluded.routing_policy,
			routing_score=excluded.routing_score,
			routing_candidate_count=excluded.routing_candidate_count,
			routing_failure_reason=excluded.routing_failure_reason
	`,
		path,
		traceID,
		info.ModTime().UnixNano(),
		info.Size(),
		header.Version,
		header.Meta.RequestID,
		header.Meta.Time.UTC().Format(timeLayout),
		header.Meta.Model,
		header.Meta.Provider,
		header.Meta.Operation,
		header.Meta.Endpoint,
		header.Meta.URL,
		header.Meta.Method,
		header.Meta.StatusCode,
		header.Meta.DurationMs,
		header.Meta.TTFTMs,
		header.Meta.ClientIP,
		header.Meta.ContentLength,
		header.Meta.Error,
		header.Usage.PromptTokens,
		header.Usage.CompletionTokens,
		header.Usage.TotalTokens,
		cachedTokens,
		header.Layout.ReqHeaderLen,
		header.Layout.ReqBodyLen,
		header.Layout.ResHeaderLen,
		header.Layout.ResBodyLen,
		boolToInt(header.Layout.IsStream),
		grouping.SessionID,
		grouping.SessionSource,
		grouping.WindowID,
		grouping.ClientRequestID,
		header.Meta.SelectedUpstreamID,
		header.Meta.SelectedUpstreamBaseURL,
		header.Meta.SelectedUpstreamProviderPreset,
		header.Meta.RoutingPolicy,
		header.Meta.RoutingScore,
		header.Meta.RoutingCandidateCount,
		header.Meta.RoutingFailureReason,
	)

	return err
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func (s *Store) Sync() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	freshness, err := s.loadFreshness()
	if err != nil {
		return err
	}

	return filepath.Walk(s.outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if path == s.dbPath || strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".http") {
			return nil
		}

		if record, ok := freshness[path]; ok && record.modTimeNs == info.ModTime().UnixNano() && record.fileSize == info.Size() {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		parsed, err := recordfile.ParsePrelude(content)
		if err != nil {
			if shouldSkipIncompleteRecord(content, err) {
				return nil
			}
			return fmt.Errorf("parse %s: %w", path, err)
		}

		grouping, err := ExtractGroupingInfo(content, parsed)
		if err != nil {
			return fmt.Errorf("extract grouping %s: %w", path, err)
		}

		return s.UpsertLogWithGrouping(path, parsed.Header, grouping)
	})
}

type freshnessRecord struct {
	modTimeNs int64
	fileSize  int64
}

func (s *Store) loadFreshness() (map[string]freshnessRecord, error) {
	rows, err := s.db.Query(`SELECT path, mod_time_ns, file_size FROM logs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	freshness := map[string]freshnessRecord{}
	for rows.Next() {
		var (
			path      string
			modTimeNs int64
			fileSize  int64
		)
		if err := rows.Scan(&path, &modTimeNs, &fileSize); err != nil {
			return nil, err
		}
		freshness[path] = freshnessRecord{modTimeNs: modTimeNs, fileSize: fileSize}
	}
	return freshness, rows.Err()
}

func shouldSkipIncompleteRecord(content []byte, err error) bool {
	if err == nil {
		return false
	}

	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return true
	}

	if bytes.HasPrefix(trimmed, []byte(recordfile.FileMagic)) {
		errText := err.Error()
		return strings.Contains(errText, "failed to read prelude") ||
			strings.Contains(errText, "missing v3 meta line") ||
			strings.Contains(errText, "invalid v3")
	}

	httpMethods := [][]byte{
		[]byte("GET "),
		[]byte("POST "),
		[]byte("PUT "),
		[]byte("PATCH "),
		[]byte("DELETE "),
		[]byte("HEAD "),
		[]byte("OPTIONS "),
	}
	for _, method := range httpMethods {
		if bytes.HasPrefix(trimmed, method) {
			return true
		}
	}

	return false
}

func (s *Store) Reset() error {
	_, err := s.client.TraceLog.Delete().Exec(context.Background())
	return err
}

func (s *Store) Rebuild() (int, error) {
	if err := s.Reset(); err != nil {
		return 0, err
	}
	if err := s.Sync(); err != nil {
		return 0, err
	}

	count, err := s.client.TraceLog.Query().Count(context.Background())
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) lookupOrCreateTraceID(path string) (string, error) {
	traceID, err := s.client.TraceLog.Query().
		Where(tracelog.IDEQ(path)).
		Select(tracelog.FieldTraceID).
		String(context.Background())
	switch {
	case err == nil && traceID != "":
		return traceID, nil
	case err == nil:
		return uuid.NewString(), nil
	case dao.IsNotFound(err):
		return uuid.NewString(), nil
	default:
		return "", err
	}
}

func (s *Store) ListRecent(limit int) ([]LogEntry, error) {
	rows, err := s.client.TraceLog.Query().
		Order(tracelog.ByRecordedAt(entsql.OrderDesc())).
		Limit(limit).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	entries := make([]LogEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, logEntryFromTraceLog(row))
	}
	return entries, nil
}

func (s *Store) ListPage(page int, pageSize int, filter ListFilter) (ListPageResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	ctx := context.Background()
	predicates := buildTraceLogPredicates(filter)
	total, err := s.client.TraceLog.Query().Where(predicates...).Count(ctx)
	if err != nil {
		return ListPageResult{}, err
	}

	offset := (page - 1) * pageSize
	rows, err := s.client.TraceLog.Query().
		Where(predicates...).
		Order(tracelog.ByRecordedAt(entsql.OrderDesc())).
		Limit(pageSize).
		Offset(offset).
		All(ctx)
	if err != nil {
		return ListPageResult{}, err
	}

	result := ListPageResult{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}
	for _, row := range rows {
		result.Items = append(result.Items, logEntryFromTraceLog(row))
	}
	if total == 0 {
		result.TotalPages = 0
		return result, nil
	}
	result.TotalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	return result, nil
}

func (s *Store) GetByID(traceID string) (LogEntry, error) {
	row, err := s.client.TraceLog.Query().
		Where(tracelog.TraceIDEQ(traceID)).
		Only(context.Background())
	if err != nil {
		if dao.IsNotFound(err) {
			return LogEntry{}, sql.ErrNoRows
		}
		return LogEntry{}, err
	}
	return logEntryFromTraceLog(row), nil
}

func (s *Store) GetByRequestID(requestID string) (LogEntry, error) {
	row, err := s.client.TraceLog.Query().
		Where(tracelog.RequestIDEQ(requestID)).
		Order(tracelog.ByRecordedAt(entsql.OrderDesc()), tracelog.ByTraceID(entsql.OrderDesc())).
		First(context.Background())
	if err != nil {
		if dao.IsNotFound(err) {
			return LogEntry{}, sql.ErrNoRows
		}
		return LogEntry{}, err
	}
	return logEntryFromTraceLog(row), nil
}

func (s *Store) SaveObservation(obs observe.TraceObservation) error {
	if obs.TraceID == "" {
		return fmt.Errorf("save observation: trace id is required")
	}
	now := time.Now().UTC()
	warningsJSON, err := json.Marshal(obs.Warnings)
	if err != nil {
		return err
	}
	summaryJSON, err := json.Marshal(observationSummaryJSON(obs))
	if err != nil {
		return err
	}
	nodes := observationFlatNodes(obs)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO parser_versions (parser, version, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(parser, version) DO NOTHING
	`, obs.Parser, obs.ParserVersion, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO trace_observations (
			trace_id, parser, parser_version, status, provider, operation, model,
			summary_json, warnings_json, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(trace_id) DO UPDATE SET
			parser=excluded.parser,
			parser_version=excluded.parser_version,
			status=excluded.status,
			provider=excluded.provider,
			operation=excluded.operation,
			model=excluded.model,
			summary_json=excluded.summary_json,
			warnings_json=excluded.warnings_json,
			updated_at=excluded.updated_at
	`, obs.TraceID, obs.Parser, obs.ParserVersion, string(obs.Status), obs.Provider, obs.Operation, obs.Model, string(summaryJSON), string(warningsJSON), now, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM semantic_nodes WHERE trace_id = ?`, obs.TraceID); err != nil {
		return err
	}
	for _, row := range nodes {
		nodeJSON, err := json.Marshal(row.Node.JSON)
		if err != nil {
			return err
		}
		rawJSON, err := json.Marshal(row.Node.Raw)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO semantic_nodes (
				trace_id, node_id, parent_node_id, provider_type, normalized_type, role,
				path, node_index, depth, text_preview, json, raw, raw_ref, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, obs.TraceID, row.Node.ID, row.ParentID, row.Node.ProviderType, string(row.Node.NormalizedType), row.Node.Role,
			row.Node.Path, row.Node.Index, row.Depth, textPreview(row.Node.Text, 240), string(nodeJSON), string(rawJSON), "", now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO parse_jobs (trace_id, status, attempts, created_at, updated_at)
		VALUES (?, ?, 1, ?, ?)
	`, obs.TraceID, string(obs.Status), now, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetObservationSummary(traceID string) (ObservationSummary, error) {
	var summary ObservationSummary
	var createdAt, updatedAt any
	err := s.db.QueryRow(`
		SELECT trace_id, parser, parser_version, status, provider, operation, model,
			summary_json, warnings_json, created_at, updated_at
		FROM trace_observations
		WHERE trace_id = ?
	`, traceID).Scan(
		&summary.TraceID,
		&summary.Parser,
		&summary.ParserVersion,
		&summary.Status,
		&summary.Provider,
		&summary.Operation,
		&summary.Model,
		&summary.SummaryJSON,
		&summary.WarningsJSON,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return ObservationSummary{}, err
	}
	if summary.CreatedAt, err = timeParseValue(createdAt); err != nil {
		return ObservationSummary{}, err
	}
	if summary.UpdatedAt, err = timeParseValue(updatedAt); err != nil {
		return ObservationSummary{}, err
	}
	return summary, nil
}

func (s *Store) ListSemanticNodes(traceID string) ([]observe.FlatSemanticNode, error) {
	rows, err := s.db.Query(`
		SELECT node_id, parent_node_id, provider_type, normalized_type, role, path,
			node_index, depth, text_preview, json, raw
		FROM semantic_nodes
		WHERE trace_id = ?
		ORDER BY depth ASC, node_index ASC, id ASC
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []observe.FlatSemanticNode
	for rows.Next() {
		var row observe.FlatSemanticNode
		var normalized string
		var nodeJSON, rawJSON string
		if err := rows.Scan(
			&row.Node.ID,
			&row.ParentID,
			&row.Node.ProviderType,
			&normalized,
			&row.Node.Role,
			&row.Node.Path,
			&row.Node.Index,
			&row.Depth,
			&row.Node.Text,
			&nodeJSON,
			&rawJSON,
		); err != nil {
			return nil, err
		}
		row.Node.ParentID = row.ParentID
		row.Node.NormalizedType = observe.NormalizedType(normalized)
		if nodeJSON != "" && nodeJSON != "null" {
			row.Node.JSON = json.RawMessage(nodeJSON)
		}
		if rawJSON != "" && rawJSON != "null" {
			row.Node.Raw = json.RawMessage(rawJSON)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) GetObservation(traceID string) (observe.TraceObservation, error) {
	summary, err := s.GetObservationSummary(traceID)
	if err != nil {
		return observe.TraceObservation{}, err
	}
	nodes, err := s.ListSemanticNodes(traceID)
	if err != nil {
		return observe.TraceObservation{}, err
	}
	var warnings []observe.ParseWarning
	if strings.TrimSpace(summary.WarningsJSON) != "" {
		_ = json.Unmarshal([]byte(summary.WarningsJSON), &warnings)
	}
	return observe.TraceObservation{
		TraceID:       summary.TraceID,
		Provider:      summary.Provider,
		Operation:     summary.Operation,
		Model:         summary.Model,
		Parser:        summary.Parser,
		ParserVersion: summary.ParserVersion,
		Status:        observe.ParseStatus(summary.Status),
		Warnings:      warnings,
		Response: observe.ObservationResponse{
			Nodes: observe.RebuildNodeTree(nodes),
		},
	}, nil
}

func (s *Store) EnqueueParseJob(traceID string) error {
	if strings.TrimSpace(traceID) == "" {
		return fmt.Errorf("enqueue parse job: trace id is required")
	}
	now := time.Now().UTC()
	_, err := s.db.Exec(`
		INSERT INTO parse_jobs (trace_id, status, attempts, created_at, updated_at)
		VALUES (?, 'queued', 0, ?, ?)
	`, traceID, now, now)
	return err
}

func (s *Store) ListParseJobs(status string, limit int) ([]ParseJobRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, trace_id, status, attempts, last_error, created_at, updated_at
		FROM parse_jobs
		WHERE status = ?
		ORDER BY updated_at ASC, id ASC
		LIMIT ?
	`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ParseJobRecord
	for rows.Next() {
		var job ParseJobRecord
		var createdAt, updatedAt any
		if err := rows.Scan(&job.ID, &job.TraceID, &job.Status, &job.Attempts, &job.LastError, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		var err error
		if job.CreatedAt, err = timeParseValue(createdAt); err != nil {
			return nil, err
		}
		if job.UpdatedAt, err = timeParseValue(updatedAt); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) MarkParseJobRunning(id int64) error {
	_, err := s.db.Exec(`
		UPDATE parse_jobs
		SET status = 'running', attempts = attempts + 1, updated_at = ?
		WHERE id = ?
	`, time.Now().UTC(), id)
	return err
}

func (s *Store) MarkParseJobDone(id int64) error {
	_, err := s.db.Exec(`
		UPDATE parse_jobs
		SET status = 'parsed', last_error = '', updated_at = ?
		WHERE id = ?
	`, time.Now().UTC(), id)
	return err
}

func (s *Store) MarkParseJobFailed(id int64, lastError string) error {
	_, err := s.db.Exec(`
		UPDATE parse_jobs
		SET status = 'failed', last_error = ?, updated_at = ?
		WHERE id = ?
	`, textPreview(lastError, 2000), time.Now().UTC(), id)
	return err
}

func (s *Store) SaveFindings(traceID string, findings []observe.Finding) error {
	if strings.TrimSpace(traceID) == "" {
		return fmt.Errorf("save findings: trace id is required")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM trace_findings WHERE trace_id = ?`, traceID); err != nil {
		return err
	}
	for _, finding := range findings {
		if finding.ID == "" {
			return fmt.Errorf("save findings: finding id is required")
		}
		if finding.CreatedAt.IsZero() {
			finding.CreatedAt = now
		}
		if finding.TraceID == "" {
			finding.TraceID = traceID
		}
		if _, err := tx.Exec(`
			INSERT INTO trace_findings (
				trace_id, finding_id, category, severity, confidence, title, description,
				evidence_path, evidence_excerpt, node_id, detector, detector_version, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, finding.TraceID, finding.ID, finding.Category, string(finding.Severity), finding.Confidence, finding.Title,
			finding.Description, finding.EvidencePath, textPreview(finding.EvidenceExcerpt, 500), finding.NodeID,
			finding.Detector, finding.DetectorVersion, finding.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListFindings(traceID string, filter FindingFilter) ([]observe.Finding, error) {
	if strings.TrimSpace(traceID) == "" {
		return nil, fmt.Errorf("list findings: trace id is required")
	}
	query := `
		SELECT finding_id, trace_id, category, severity, confidence, title, description,
			evidence_path, evidence_excerpt, node_id, detector, detector_version, created_at
		FROM trace_findings
		WHERE trace_id = ?
	`
	args := []any{traceID}
	if category := strings.TrimSpace(filter.Category); category != "" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	if severity := strings.TrimSpace(filter.Severity); severity != "" {
		query += ` AND severity = ?`
		args = append(args, severity)
	}
	query += ` ORDER BY id ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []observe.Finding
	for rows.Next() {
		var finding observe.Finding
		var severity string
		var createdAt any
		if err := rows.Scan(&finding.ID, &finding.TraceID, &finding.Category, &severity, &finding.Confidence, &finding.Title,
			&finding.Description, &finding.EvidencePath, &finding.EvidenceExcerpt, &finding.NodeID,
			&finding.Detector, &finding.DetectorVersion, &createdAt); err != nil {
			return nil, err
		}
		finding.Severity = observe.Severity(severity)
		if finding.CreatedAt, err = timeParseValue(createdAt); err != nil {
			return nil, err
		}
		out = append(out, finding)
	}
	return out, rows.Err()
}

func (s *Store) ListAllFindings(filter FindingFilter, limit int) ([]observe.Finding, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT finding_id, trace_id, category, severity, confidence, title, description,
			evidence_path, evidence_excerpt, node_id, detector, detector_version, created_at
		FROM trace_findings
		WHERE 1 = 1
	`
	var args []any
	if category := strings.TrimSpace(filter.Category); category != "" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	if severity := strings.TrimSpace(filter.Severity); severity != "" {
		query += ` AND severity = ?`
		args = append(args, severity)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []observe.Finding
	for rows.Next() {
		var finding observe.Finding
		var severity string
		var createdAt any
		if err := rows.Scan(&finding.ID, &finding.TraceID, &finding.Category, &severity, &finding.Confidence, &finding.Title,
			&finding.Description, &finding.EvidencePath, &finding.EvidenceExcerpt, &finding.NodeID,
			&finding.Detector, &finding.DetectorVersion, &createdAt); err != nil {
			return nil, err
		}
		finding.Severity = observe.Severity(severity)
		if finding.CreatedAt, err = timeParseValue(createdAt); err != nil {
			return nil, err
		}
		out = append(out, finding)
	}
	return out, rows.Err()
}

func (s *Store) SaveAnalysisRun(run AnalysisRunRecord) (int64, error) {
	if strings.TrimSpace(run.Kind) == "" {
		return 0, fmt.Errorf("save analysis run: kind is required")
	}
	if strings.TrimSpace(run.Analyzer) == "" {
		return 0, fmt.Errorf("save analysis run: analyzer is required")
	}
	if strings.TrimSpace(run.Status) == "" {
		run.Status = "completed"
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.Exec(`
		INSERT INTO analysis_runs (
			trace_id, session_id, kind, analyzer, analyzer_version, model, input_ref, output_json, status, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.TraceID, run.SessionID, run.Kind, run.Analyzer, run.AnalyzerVersion, run.Model, run.InputRef, run.OutputJSON, run.Status, run.CreatedAt)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) ListAnalysisRuns(sessionID string, traceID string, kind string, limit int) ([]AnalysisRunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT id, trace_id, session_id, kind, analyzer, analyzer_version, model, input_ref, output_json, status, created_at
		FROM analysis_runs
		WHERE 1 = 1
	`
	var args []any
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	if traceID = strings.TrimSpace(traceID); traceID != "" {
		query += ` AND trace_id = ?`
		args = append(args, traceID)
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		query += ` AND kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AnalysisRunRecord
	for rows.Next() {
		var run AnalysisRunRecord
		var createdAt any
		if err := rows.Scan(&run.ID, &run.TraceID, &run.SessionID, &run.Kind, &run.Analyzer, &run.AnalyzerVersion,
			&run.Model, &run.InputRef, &run.OutputJSON, &run.Status, &createdAt); err != nil {
			return nil, err
		}
		if run.CreatedAt, err = timeParseValue(createdAt); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) ListSessionPage(page int, pageSize int, filter ListFilter) (SessionPageResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}

	whereSQL, whereArgs := buildLogFilterClause(filter, "s")
	sessionWhere := `s.session_id <> ''`
	if whereSQL != "" {
		sessionWhere += " AND " + whereSQL
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM (SELECT session_id FROM logs s WHERE `+sessionWhere+` GROUP BY session_id)`, whereArgs...).Scan(&total); err != nil {
		return SessionPageResult{}, err
	}

	offset := (page - 1) * pageSize
	queryArgs := append([]any{}, whereArgs...)
	queryArgs = append(queryArgs, pageSize, offset)
	listSQL := `
		SELECT
			s.session_id,
			MIN(s.session_source) AS session_source,
			COUNT(*) AS request_count,
			MIN(s.recorded_at) AS first_seen,
			MAX(s.recorded_at) AS last_seen,
			COALESCE((
				SELECT model FROM logs l2
				WHERE l2.session_id = s.session_id
				ORDER BY l2.recorded_at DESC, l2.trace_id DESC
				LIMIT 1
			), '') AS last_model,
			COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN s.provider <> '' THEN s.provider END), '') AS providers,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS success_request,
			COALESCE(SUM(CASE WHEN s.status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS failed_request,
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				100.0 * SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*)
			END AS success_rate,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.total_tokens ELSE 0 END), 0) AS total_tokens,
			COALESCE(AVG(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.ttft_ms END), 0) AS avg_ttft,
			COALESCE(SUM(s.duration_ms), 0) AS total_duration,
			COALESCE(SUM(CASE WHEN s.is_stream = 1 THEN 1 ELSE 0 END), 0) AS stream_count
		FROM logs s
		WHERE ` + sessionWhere + `
		GROUP BY s.session_id
		ORDER BY MAX(s.recorded_at) DESC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.Query(listSQL, queryArgs...)
	if err != nil {
		return SessionPageResult{}, err
	}
	defer rows.Close()

	result := SessionPageResult{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
	}
	for rows.Next() {
		summary, err := scanSessionSummary(rows)
		if err != nil {
			return SessionPageResult{}, err
		}
		result.Items = append(result.Items, summary)
	}
	if err := rows.Err(); err != nil {
		return SessionPageResult{}, err
	}
	if total == 0 {
		return result, nil
	}
	result.TotalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	return result, nil
}

func (s *Store) GetSession(sessionID string) (SessionSummary, error) {
	row := s.db.QueryRow(`
		SELECT
			s.session_id,
			MIN(s.session_source) AS session_source,
			COUNT(*) AS request_count,
			MIN(s.recorded_at) AS first_seen,
			MAX(s.recorded_at) AS last_seen,
			COALESCE((
				SELECT model FROM logs l2
				WHERE l2.session_id = s.session_id
				ORDER BY l2.recorded_at DESC, l2.trace_id DESC
				LIMIT 1
			), '') AS last_model,
			COALESCE(GROUP_CONCAT(DISTINCT CASE WHEN s.provider <> '' THEN s.provider END), '') AS providers,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS success_request,
			COALESCE(SUM(CASE WHEN s.status_code NOT BETWEEN 200 AND 299 THEN 1 ELSE 0 END), 0) AS failed_request,
			CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				100.0 * SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) / COUNT(*)
			END AS success_rate,
			COALESCE(SUM(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.total_tokens ELSE 0 END), 0) AS total_tokens,
			COALESCE(AVG(CASE WHEN s.status_code BETWEEN 200 AND 299 THEN s.ttft_ms END), 0) AS avg_ttft,
			COALESCE(SUM(s.duration_ms), 0) AS total_duration,
			COALESCE(SUM(CASE WHEN s.is_stream = 1 THEN 1 ELSE 0 END), 0) AS stream_count
		FROM logs s
		WHERE s.session_id = ?
		GROUP BY s.session_id
	`, sessionID)
	return scanSessionSummary(row)
}

func (s *Store) ListTracesBySession(sessionID string) ([]LogEntry, error) {
	rows, err := s.db.Query(`
		SELECT
			trace_id, path, version, request_id, recorded_at, model, provider, operation, endpoint, url, method, status_code,
			duration_ms, ttft_ms, client_ip, content_length, error_text,
			prompt_tokens, completion_tokens, total_tokens, cached_tokens,
			req_header_len, req_body_len, res_header_len, res_body_len, is_stream,
			session_id, session_source, window_id, client_request_id,
			selected_upstream_id, selected_upstream_base_url, selected_upstream_provider_preset,
			routing_policy, routing_score, routing_candidate_count, routing_failure_reason
		FROM logs
		WHERE session_id = ?
		ORDER BY recorded_at DESC, trace_id DESC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) PathByID(traceID string) (string, error) {
	path, err := s.client.TraceLog.Query().
		Where(tracelog.TraceIDEQ(traceID)).
		OnlyID(context.Background())
	if dao.IsNotFound(err) {
		return "", sql.ErrNoRows
	}
	return path, err
}

func (s *Store) Stats() (Stats, error) {
	ctx := context.Background()
	total, err := s.client.TraceLog.Query().Count(ctx)
	if err != nil {
		return Stats{}, err
	}

	successQuery := s.client.TraceLog.Query().
		Where(tracelog.StatusCodeGTE(200), tracelog.StatusCodeLT(300))
	successCount, err := successQuery.Clone().Count(ctx)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		TotalRequest:   total,
		SuccessRequest: successCount,
		FailedRequest:  total - successCount,
	}
	if total > 0 {
		stats.SuccessRate = 100.0 * float64(successCount) / float64(total)
	}
	if successCount == 0 {
		return stats, nil
	}

	avgTTFT, err := successQuery.Clone().
		Aggregate(dao.Mean(tracelog.FieldTtftMs)).
		Float64(ctx)
	if err != nil {
		return Stats{}, err
	}
	totalTokens, err := successQuery.Clone().
		Aggregate(dao.Sum(tracelog.FieldTotalTokens)).
		Int(ctx)
	if err != nil {
		return Stats{}, err
	}
	stats.AvgTTFT = int(math.Round(avgTTFT))
	stats.TotalTokens = totalTokens
	return stats, nil
}

func logEntryFromTraceLog(row *dao.TraceLog) LogEntry {
	entry := LogEntry{
		ID:              row.TraceID,
		LogPath:         row.ID,
		SessionID:       row.SessionID,
		SessionSource:   row.SessionSource,
		WindowID:        row.WindowID,
		ClientRequestID: row.ClientRequestID,
	}
	entry.Header.Version = row.Version
	entry.Header.Meta.RequestID = row.RequestID
	entry.Header.Meta.Time = row.RecordedAt
	entry.Header.Meta.Model = row.Model
	entry.Header.Meta.Provider = row.Provider
	entry.Header.Meta.Operation = row.Operation
	entry.Header.Meta.Endpoint = row.Endpoint
	entry.Header.Meta.URL = row.URL
	entry.Header.Meta.Method = row.Method
	entry.Header.Meta.StatusCode = row.StatusCode
	entry.Header.Meta.DurationMs = row.DurationMs
	entry.Header.Meta.TTFTMs = row.TtftMs
	entry.Header.Meta.ClientIP = row.ClientIP
	entry.Header.Meta.ContentLength = row.ContentLength
	entry.Header.Meta.Error = row.ErrorText
	entry.Header.Meta.SelectedUpstreamID = row.SelectedUpstreamID
	entry.Header.Meta.SelectedUpstreamBaseURL = row.SelectedUpstreamBaseURL
	entry.Header.Meta.SelectedUpstreamProviderPreset = row.SelectedUpstreamProviderPreset
	entry.Header.Meta.RoutingPolicy = row.RoutingPolicy
	entry.Header.Meta.RoutingScore = row.RoutingScore
	entry.Header.Meta.RoutingCandidateCount = row.RoutingCandidateCount
	entry.Header.Meta.RoutingFailureReason = row.RoutingFailureReason
	entry.Header.Usage.PromptTokens = row.PromptTokens
	entry.Header.Usage.CompletionTokens = row.CompletionTokens
	entry.Header.Usage.TotalTokens = row.TotalTokens
	if row.CachedTokens > 0 {
		entry.Header.Usage.PromptTokenDetails = &recordfile.PromptTokenDetails{CachedTokens: row.CachedTokens}
	}
	entry.Header.Layout.ReqHeaderLen = row.ReqHeaderLen
	entry.Header.Layout.ReqBodyLen = row.ReqBodyLen
	entry.Header.Layout.ResHeaderLen = row.ResHeaderLen
	entry.Header.Layout.ResBodyLen = row.ResBodyLen
	entry.Header.Layout.IsStream = row.IsStream
	return entry
}

func upstreamTargetRecordFromEnt(row *dao.UpstreamTarget) UpstreamTargetRecord {
	record := UpstreamTargetRecord{
		ID:                row.ID,
		BaseURL:           row.BaseURL,
		ProviderPreset:    row.ProviderPreset,
		ProtocolFamily:    row.ProtocolFamily,
		RoutingProfile:    row.RoutingProfile,
		Enabled:           row.Enabled,
		Priority:          row.Priority,
		Weight:            row.Weight,
		CapacityHint:      row.CapacityHint,
		LastRefreshStatus: row.LastRefreshStatus,
		LastRefreshError:  row.LastRefreshError,
	}
	if row.LastRefreshAt != nil {
		record.LastRefreshAt = *row.LastRefreshAt
	}
	return record
}

func upstreamModelRecordFromEnt(row *dao.UpstreamModel) UpstreamModelRecord {
	return UpstreamModelRecord{
		UpstreamID: row.UpstreamID,
		Model:      row.Model,
		Source:     row.Source,
		SeenAt:     row.SeenAt,
	}
}

func channelConfigRecordFromEnt(row *dao.ChannelConfig) ChannelConfigRecord {
	record := ChannelConfigRecord{
		ID:                 row.ID,
		Name:               row.Name,
		Description:        row.Description,
		BaseURL:            row.BaseURL,
		ProviderPreset:     row.ProviderPreset,
		ProtocolFamily:     row.ProtocolFamily,
		RoutingProfile:     row.RoutingProfile,
		APIVersion:         row.APIVersion,
		Deployment:         row.Deployment,
		Project:            row.Project,
		Location:           row.Location,
		ModelResource:      row.ModelResource,
		APIKeyHint:         row.APIKeyHint,
		HeadersJSON:        row.HeadersJSON,
		Enabled:            row.Enabled,
		Priority:           row.Priority,
		Weight:             row.Weight,
		CapacityHint:       row.CapacityHint,
		ModelDiscovery:     row.ModelDiscovery,
		AllowUnknownModels: row.AllowUnknownModels,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
		LastProbeStatus:    row.LastProbeStatus,
		LastProbeError:     row.LastProbeError,
	}
	if row.APIKeyCiphertext != nil {
		record.APIKeyCiphertext = append([]byte(nil), (*row.APIKeyCiphertext)...)
	}
	if row.LastProbeAt != nil {
		record.LastProbeAt = *row.LastProbeAt
	}
	return record
}

func (s *Store) channelConfigRecordFromEnt(row *dao.ChannelConfig) (ChannelConfigRecord, error) {
	record := channelConfigRecordFromEnt(row)
	if len(record.APIKeyCiphertext) > 0 {
		plaintext, err := s.decryptSecretBytes(record.APIKeyCiphertext)
		if err != nil {
			return ChannelConfigRecord{}, fmt.Errorf("decrypt channel %s api key: %w", row.ID, err)
		}
		record.APIKeyCiphertext = plaintext
	}
	if strings.TrimSpace(record.HeadersJSON) != "" {
		headers, err := s.decryptHeadersJSON(record.HeadersJSON)
		if err != nil {
			return ChannelConfigRecord{}, fmt.Errorf("decrypt channel %s headers: %w", row.ID, err)
		}
		record.HeadersJSON = headers
	}
	return record, nil
}

func channelModelRecordFromEnt(row *dao.ChannelModel) ChannelModelRecord {
	record := ChannelModelRecord{
		ChannelID:            row.ChannelID,
		Model:                row.Model,
		DisplayName:          row.DisplayName,
		Source:               row.Source,
		Enabled:              row.Enabled,
		InputModalitiesJSON:  row.InputModalitiesJSON,
		OutputModalitiesJSON: row.OutputModalitiesJSON,
		RawModelJSON:         row.RawModelJSON,
		FirstSeenAt:          row.FirstSeenAt,
		LastSeenAt:           row.LastSeenAt,
	}
	if row.SupportsResponses != nil {
		value := *row.SupportsResponses
		record.SupportsResponses = &value
	}
	if row.SupportsChatCompletions != nil {
		value := *row.SupportsChatCompletions
		record.SupportsChatCompletions = &value
	}
	if row.SupportsEmbeddings != nil {
		value := *row.SupportsEmbeddings
		record.SupportsEmbeddings = &value
	}
	if row.ContextWindow != nil {
		value := *row.ContextWindow
		record.ContextWindow = &value
	}
	if row.LastProbeAt != nil {
		record.LastProbeAt = *row.LastProbeAt
	}
	return record
}

func channelProbeRunRecordFromEnt(row *dao.ChannelProbeRun) ChannelProbeRunRecord {
	record := ChannelProbeRunRecord{
		ID:                 row.ID,
		ChannelID:          row.ChannelID,
		Status:             row.Status,
		StartedAt:          row.StartedAt,
		DurationMs:         row.DurationMs,
		DiscoveredCount:    row.DiscoveredCount,
		EnabledCount:       row.EnabledCount,
		Endpoint:           row.Endpoint,
		StatusCode:         row.StatusCode,
		ErrorText:          row.ErrorText,
		RequestMetaJSON:    row.RequestMetaJSON,
		ResponseSampleJSON: row.ResponseSampleJSON,
	}
	if row.CompletedAt != nil {
		record.CompletedAt = *row.CompletedAt
	}
	return record
}

func defaultJSON(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func datasetRecordFromEnt(row *dao.Dataset, exampleCount int) DatasetRecord {
	return DatasetRecord{
		ID:           row.ID,
		Name:         row.Name,
		Description:  row.Description,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		ExampleCount: exampleCount,
	}
}

func evalRunRecordFromEnt(row *dao.EvalRun) EvalRunRecord {
	return EvalRunRecord{
		ID:           row.ID,
		DatasetID:    row.DatasetID,
		SourceType:   row.SourceType,
		SourceID:     row.SourceID,
		EvaluatorSet: row.EvaluatorSet,
		CreatedAt:    row.CreatedAt,
		CompletedAt:  row.CompletedAt,
		TraceCount:   row.TraceCount,
		ScoreCount:   row.ScoreCount,
		PassCount:    row.PassCount,
		FailCount:    row.FailCount,
	}
}

func scoreRecordFromEnt(row *dao.Score) ScoreRecord {
	return ScoreRecord{
		ID:           row.ID,
		TraceID:      row.TraceID,
		SessionID:    row.SessionID,
		DatasetID:    row.DatasetID,
		EvalRunID:    row.EvalRunID,
		EvaluatorKey: row.EvaluatorKey,
		Value:        row.Value,
		Status:       row.Status,
		Label:        row.Label,
		Explanation:  row.Explanation,
		CreatedAt:    row.CreatedAt,
	}
}

func experimentRunRecordFromEnt(row *dao.ExperimentRun) ExperimentRunRecord {
	return ExperimentRunRecord{
		ID:                  row.ID,
		Name:                row.Name,
		Description:         row.Description,
		BaselineEvalRunID:   row.BaselineEvalRunID,
		CandidateEvalRunID:  row.CandidateEvalRunID,
		CreatedAt:           row.CreatedAt,
		BaselineScoreCount:  row.BaselineScoreCount,
		CandidateScoreCount: row.CandidateScoreCount,
		BaselinePassRate:    row.BaselinePassRate,
		CandidatePassRate:   row.CandidatePassRate,
		PassRateDelta:       row.PassRateDelta,
		MatchedScoreCount:   row.MatchedScoreCount,
		ImprovementCount:    row.ImprovementCount,
		RegressionCount:     row.RegressionCount,
	}
}

func scanEntry(scanner interface {
	Scan(dest ...any) error
}) (LogEntry, error) {
	var (
		entry        LogEntry
		recordedAt   any
		errorText    string
		cached       int
		isStream     int
		routingScore float64
	)

	err := scanner.Scan(
		&entry.ID,
		&entry.LogPath,
		&entry.Header.Version,
		&entry.Header.Meta.RequestID,
		&recordedAt,
		&entry.Header.Meta.Model,
		&entry.Header.Meta.Provider,
		&entry.Header.Meta.Operation,
		&entry.Header.Meta.Endpoint,
		&entry.Header.Meta.URL,
		&entry.Header.Meta.Method,
		&entry.Header.Meta.StatusCode,
		&entry.Header.Meta.DurationMs,
		&entry.Header.Meta.TTFTMs,
		&entry.Header.Meta.ClientIP,
		&entry.Header.Meta.ContentLength,
		&errorText,
		&entry.Header.Usage.PromptTokens,
		&entry.Header.Usage.CompletionTokens,
		&entry.Header.Usage.TotalTokens,
		&cached,
		&entry.Header.Layout.ReqHeaderLen,
		&entry.Header.Layout.ReqBodyLen,
		&entry.Header.Layout.ResHeaderLen,
		&entry.Header.Layout.ResBodyLen,
		&isStream,
		&entry.SessionID,
		&entry.SessionSource,
		&entry.WindowID,
		&entry.ClientRequestID,
		&entry.Header.Meta.SelectedUpstreamID,
		&entry.Header.Meta.SelectedUpstreamBaseURL,
		&entry.Header.Meta.SelectedUpstreamProviderPreset,
		&entry.Header.Meta.RoutingPolicy,
		&routingScore,
		&entry.Header.Meta.RoutingCandidateCount,
		&entry.Header.Meta.RoutingFailureReason,
	)
	if err != nil {
		return LogEntry{}, err
	}

	entry.Header.Meta.Time, err = timeParseValue(recordedAt)
	if err != nil {
		return LogEntry{}, err
	}
	entry.Header.Meta.Error = errorText
	entry.Header.Meta.RoutingScore = routingScore
	entry.Header.Layout.IsStream = isStream == 1
	if cached > 0 {
		entry.Header.Usage.PromptTokenDetails = &recordfile.PromptTokenDetails{CachedTokens: cached}
	}

	return entry, nil
}

func scanSessionSummary(scanner interface {
	Scan(dest ...any) error
}) (SessionSummary, error) {
	var (
		summary      SessionSummary
		firstSeen    any
		lastSeen     any
		providersCSV string
		avgTTFT      float64
	)
	err := scanner.Scan(
		&summary.SessionID,
		&summary.SessionSource,
		&summary.RequestCount,
		&firstSeen,
		&lastSeen,
		&summary.LastModel,
		&providersCSV,
		&summary.SuccessRequest,
		&summary.FailedRequest,
		&summary.SuccessRate,
		&summary.TotalTokens,
		&avgTTFT,
		&summary.TotalDuration,
		&summary.StreamCount,
	)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.FirstSeen, err = timeParseValue(firstSeen)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.LastSeen, err = timeParseValue(lastSeen)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.AvgTTFT = int(math.Round(avgTTFT))
	summary.Providers = splitProviders(providersCSV)
	return summary, nil
}

func timeParse(v string) (time.Time, error) {
	for _, layout := range []string{
		timeLayout,
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	} {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed, nil
		}
	}
	return time.Parse(timeLayout, v)
}

func timeParseValue(v any) (time.Time, error) {
	switch value := v.(type) {
	case time.Time:
		return value, nil
	case string:
		return timeParse(value)
	case []byte:
		return timeParse(string(value))
	case nil:
		return time.Time{}, nil
	default:
		return timeParse(fmt.Sprint(value))
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func ExtractGroupingInfo(content []byte, parsed *recordfile.ParsedPrelude) (GroupingInfo, error) {
	reqFull, _, _, _ := recordfile.ExtractSections(content, parsed)
	return extractGroupingInfoFromRequest(reqFull)
}

func extractGroupingInfoFromRequest(reqFull []byte) (GroupingInfo, error) {
	headers := parseRawRequestHeaders(reqFull)
	info := GroupingInfo{
		WindowID:        strings.TrimSpace(headers.Get("X-Codex-Window-Id")),
		ClientRequestID: strings.TrimSpace(headers.Get("X-Client-Request-Id")),
	}
	if sessionID := strings.TrimSpace(headers.Get("Session_id")); sessionID != "" {
		info.SessionID = sessionID
		info.SessionSource = "header.session_id"
		return info, nil
	}

	if rawMetadata := strings.TrimSpace(headers.Get("X-Codex-Turn-Metadata")); rawMetadata != "" {
		var metadata struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(rawMetadata), &metadata); err == nil && strings.TrimSpace(metadata.SessionID) != "" {
			info.SessionID = strings.TrimSpace(metadata.SessionID)
			info.SessionSource = "header.x_codex_turn_metadata.session_id"
			return info, nil
		}
	}

	if info.WindowID != "" {
		info.SessionID = normalizeWindowSessionID(info.WindowID)
		if info.SessionID != "" {
			info.SessionSource = "header.x_codex_window_id"
			return info, nil
		}
	}

	info.SessionSource = "none"
	return info, nil
}

func parseRawRequestHeaders(reqFull []byte) textproto.MIMEHeader {
	headers := make(textproto.MIMEHeader)
	lines := strings.Split(string(reqFull), "\r\n")
	for idx, line := range lines {
		if idx == 0 || line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		headers.Add(textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name)), strings.TrimSpace(value))
	}
	return headers
}

func normalizeWindowSessionID(windowID string) string {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return ""
	}
	sessionID, _, found := strings.Cut(windowID, ":")
	if !found {
		return windowID
	}
	return strings.TrimSpace(sessionID)
}

func splitProviders(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	seen := map[string]struct{}{}
	var providers []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		providers = append(providers, part)
	}
	sort.Strings(providers)
	return providers
}

func buildTraceLogPredicates(filter ListFilter) []predicate.TraceLog {
	var predicates []predicate.TraceLog
	if provider := strings.TrimSpace(filter.Provider); provider != "" {
		predicates = append(predicates, tracelog.ProviderEqualFold(provider))
	}
	if model := strings.TrimSpace(filter.Model); model != "" {
		predicates = append(predicates, tracelog.ModelContainsFold(model))
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		predicates = append(predicates, tracelog.Or(
			tracelog.SessionIDContainsFold(query),
			tracelog.TraceIDContainsFold(query),
			tracelog.ModelContainsFold(query),
			tracelog.ProviderContainsFold(query),
			tracelog.EndpointContainsFold(query),
			tracelog.URLContainsFold(query),
		))
	}
	return predicates
}

func observationSummaryJSON(obs observe.TraceObservation) map[string]any {
	return map[string]any{
		"request_nodes":  len(obs.Request.Nodes),
		"response_nodes": len(obs.Response.Nodes),
		"stream_events":  len(obs.Stream.Events),
		"tool_calls":     len(obs.Tools.Calls),
		"tool_results":   len(obs.Tools.Results),
		"findings":       len(obs.Findings),
	}
}

func observationFlatNodes(obs observe.TraceObservation) []observe.FlatSemanticNode {
	var roots []observe.SemanticNode
	roots = append(roots, obs.Request.Nodes...)
	roots = append(roots, obs.Response.Nodes...)
	roots = append(roots, obs.Stream.AccumulatedToolCalls...)
	return observe.FlattenNodes(roots)
}

func textPreview(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}

func buildLogFilterClause(filter ListFilter, alias string) (string, []any) {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}

	var (
		clauses []string
		args    []any
	)

	if provider := strings.TrimSpace(filter.Provider); provider != "" {
		clauses = append(clauses, `LOWER(`+column("provider")+`) = LOWER(?)`)
		args = append(args, provider)
	}
	if model := strings.TrimSpace(filter.Model); model != "" {
		clauses = append(clauses, `LOWER(`+column("model")+`) LIKE LOWER(?)`)
		args = append(args, "%"+escapeLike(model)+"%")
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		pattern := "%" + escapeLike(query) + "%"
		clauses = append(clauses, `(
			LOWER(`+column("session_id")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("trace_id")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("model")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("provider")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("endpoint")+`) LIKE LOWER(?) ESCAPE '\' OR
			LOWER(`+column("url")+`) LIKE LOWER(?) ESCAPE '\'
		)`)
		for range 6 {
			args = append(args, pattern)
		}
	}

	return strings.Join(clauses, " AND "), args
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func (s *Store) backfillGrouping() error {
	rows, err := s.db.Query(`SELECT path FROM logs WHERE session_source = '' OR session_source = 'none'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		parsed, err := recordfile.ParsePrelude(content)
		if err != nil {
			if shouldSkipIncompleteRecord(content, err) {
				continue
			}
			return err
		}
		grouping, err := ExtractGroupingInfo(content, parsed)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(
			`UPDATE logs SET session_id = ?, session_source = ?, window_id = ?, client_request_id = ? WHERE path = ?`,
			grouping.SessionID,
			grouping.SessionSource,
			grouping.WindowID,
			grouping.ClientRequestID,
			path,
		); err != nil {
			return err
		}
	}
	return nil
}
