// Package model holds the control plane's API types (mirroring
// contracts/openapi/gearbox-admin.v1.yaml#/components/schemas) and the records
// it persists. Definition specs stay json.RawMessage: the control plane stores
// them without knowing their structure; the engine owns their meaning.
package model

import (
	"encoding/json"
	"regexp"
	"time"
)

// NamePattern is the contract's Name rule.
var NamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// EnvPattern is the contract's Env path parameter rule.
var EnvPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// Kinds of versioned resources.
const (
	KindSchemas     = "schemas"
	KindDecisions   = "decisions"
	KindSegments    = "segments"
	KindLookups     = "lookups"
	KindGates       = "gates"
	KindExperiments = "experiments"
	KindLayers      = "layers"
)

// AllKinds in a stable order (dependencies first).
var AllKinds = []string{KindSchemas, KindSegments, KindLookups, KindGates, KindLayers, KindExperiments, KindDecisions}

// ValidKind reports whether k is a resource kind.
func ValidKind(k string) bool {
	for _, x := range AllKinds {
		if x == k {
			return true
		}
	}
	return false
}

// SpecSchemaName maps a kind to its component schema in the contract.
func SpecSchemaName(kind string) string {
	switch kind {
	case KindSchemas:
		return "SchemaSpec"
	case KindDecisions:
		return "DecisionSpec"
	case KindSegments:
		return "SegmentSpec"
	case KindLookups:
		return "LookupSpec"
	case KindGates:
		return "GateSpec"
	case KindExperiments:
		return "ExperimentSpec"
	case KindLayers:
		return "LayerSpec"
	}
	return ""
}

// Roles in increasing order of power.
const (
	RoleViewer   = "viewer"
	RoleAuthor   = "author"
	RoleApprover = "approver"
	RoleAdmin    = "admin"
)

// RoleRank orders roles; unknown roles rank below viewer.
func RoleRank(r string) int {
	switch r {
	case RoleViewer:
		return 1
	case RoleAuthor:
		return 2
	case RoleApprover:
		return 3
	case RoleAdmin:
		return 4
	}
	return 0
}

// ---------------------------------------------------------------- members

type Member struct {
	ID    string   `json:"id"`
	Email string   `json:"email"`
	Name  string   `json:"name,omitempty"`
	Role  string   `json:"role"`
	Apps  []string `json:"apps"`
}

type MemberInput struct {
	Email string   `json:"email"`
	Name  string   `json:"name,omitempty"`
	Role  string   `json:"role"`
	Apps  []string `json:"apps,omitempty"`
}

type Session struct {
	Member    Member `json:"member"`
	CSRFToken string `json:"csrfToken"`
}

// Token is a personal access token record (secret stored as HMAC).
type Token struct {
	ID        string    `json:"id"`
	MemberID  string    `json:"memberId"`
	Label     string    `json:"label"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// WebSession is a browser session created by the OIDC callback.
type WebSession struct {
	ID        string    `json:"id"` // HMAC of the cookie value
	MemberID  string    `json:"memberId"`
	CSRF      string    `json:"csrf"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// ---------------------------------------------------------------- apps

type App struct {
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Owners        []string  `json:"owners,omitempty"`
	Environments  []string  `json:"environments"`
	BundleLimitMB int       `json:"bundleLimitMB,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type AppCreate struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Owners       []string `json:"owners,omitempty"`
	Environments []string `json:"environments,omitempty"`
}

type AppUpdate struct {
	Description   *string   `json:"description,omitempty"`
	Owners        *[]string `json:"owners,omitempty"`
	BundleLimitMB *int      `json:"bundleLimitMB,omitempty"`
}

type EnvironmentPolicy struct {
	RequiredApprovals *int     `json:"requiredApprovals,omitempty"`
	AuthorCanApprove  *bool    `json:"authorCanApprove,omitempty"`
	AllowDataReleases *bool    `json:"allowDataReleases,omitempty"`
	LogSampleRate     *float64 `json:"logSampleRate,omitempty"`
}

// Policy is the resolved (non-pointer) policy.
type Policy struct {
	RequiredApprovals int     `json:"requiredApprovals"`
	AuthorCanApprove  bool    `json:"authorCanApprove"`
	AllowDataReleases bool    `json:"allowDataReleases"`
	LogSampleRate     float64 `json:"logSampleRate"`
}

// API returns the wire form.
func (p Policy) API() EnvironmentPolicy {
	return EnvironmentPolicy{RequiredApprovals: &p.RequiredApprovals, AuthorCanApprove: &p.AuthorCanApprove, AllowDataReleases: &p.AllowDataReleases, LogSampleRate: &p.LogSampleRate}
}

// Apply merges a partial update.
func (p Policy) Apply(u EnvironmentPolicy) Policy {
	if u.RequiredApprovals != nil {
		p.RequiredApprovals = *u.RequiredApprovals
	}
	if u.AuthorCanApprove != nil {
		p.AuthorCanApprove = *u.AuthorCanApprove
	}
	if u.AllowDataReleases != nil {
		p.AllowDataReleases = *u.AllowDataReleases
	}
	if u.LogSampleRate != nil {
		p.LogSampleRate = *u.LogSampleRate
	}
	return p
}

// DefaultPolicy for a new environment by conventional name.
func DefaultPolicy(env string) Policy {
	switch env {
	case "dev":
		return Policy{RequiredApprovals: 0, AuthorCanApprove: true, AllowDataReleases: true, LogSampleRate: 1}
	case "stage":
		return Policy{RequiredApprovals: 1, AuthorCanApprove: true, AllowDataReleases: true, LogSampleRate: 0.1}
	default:
		return Policy{RequiredApprovals: 1, AuthorCanApprove: false, AllowDataReleases: false, LogSampleRate: 0.01}
	}
}

type Environment struct {
	Name        string            `json:"name"`
	Policy      EnvironmentPolicy `json:"policy"`
	Generation  int64             `json:"generation"`
	PublishedAt *time.Time        `json:"publishedAt,omitempty"`
}

// LiveRollout is the rollout part of LiveState.
type LiveRollout struct {
	FromVersion int64 `json:"fromVersion"`
	Percent     int   `json:"percent"`
}

type LiveState struct {
	Version int64        `json:"version"`
	Rollout *LiveRollout `json:"rollout,omitempty"`
}

// LiveEntry is the persisted live state of one resource in one environment.
type LiveEntry struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version int64  `json:"version"`
	// Rollout (decisions only): Version is the new version, FromVersion the
	// previous one; Percent of units get Version.
	RolloutFrom    int64  `json:"rolloutFrom,omitempty"`
	RolloutPercent int    `json:"rolloutPercent,omitempty"`
	RolloutSalt    string `json:"rolloutSalt,omitempty"`
	Rolling        bool   `json:"rolling,omitempty"`
}

// State converts to the API LiveState.
func (e LiveEntry) State() LiveState {
	s := LiveState{Version: e.Version}
	if e.Rolling {
		s.Rollout = &LiveRollout{FromVersion: e.RolloutFrom, Percent: e.RolloutPercent}
	}
	return s
}

// EnvState is the persisted state of one app environment: its policy, its
// current generation and what is live in it.
type EnvState struct {
	App         string               `json:"app"`
	Name        string               `json:"name"`
	Policy      Policy               `json:"policy"`
	Generation  int64                `json:"generation"`
	PublishedAt *time.Time           `json:"publishedAt,omitempty"`
	Live        map[string]LiveEntry `json:"live"`        // key kind/name
	KilledGates map[string]string    `json:"killedGates"` // gate -> reason
}

// Clone deep-copies the maps.
func (s EnvState) Clone() EnvState {
	c := s
	c.Live = make(map[string]LiveEntry, len(s.Live))
	for k, v := range s.Live {
		c.Live[k] = v
	}
	c.KilledGates = make(map[string]string, len(s.KilledGates))
	for k, v := range s.KilledGates {
		c.KilledGates[k] = v
	}
	return c
}

// LiveKey is the Live map key.
func LiveKey(kind, name string) string { return kind + "/" + name }

type Credential struct {
	ID          string     `json:"id"`
	Environment string     `json:"environment"`
	Label       string     `json:"label,omitempty"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"createdAt"`
	CreatedBy   string     `json:"createdBy,omitempty"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
}

// CredentialRecord is the persisted credential.
type CredentialRecord struct {
	Credential
	App  string `json:"app"`
	Hash string `json:"hash"`
}

type CredentialCreated struct {
	Credential Credential `json:"credential"`
	Secret     string     `json:"secret"`
}

// ---------------------------------------------------------------- resources

type ValidationProblem struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type ResourceRef struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version int64  `json:"version,omitempty"`
}

type ValidationResult struct {
	Valid        bool                `json:"valid"`
	Problems     []ValidationProblem `json:"problems"`
	Dependencies []ResourceRef       `json:"dependencies,omitempty"`
}

// DataRef points at large data (segment members, lookup rows) in the object
// store: data/sha256/<hex>, JSON-encoded.
type DataRef struct {
	SHA256 string `json:"sha256"`
	Count  int    `json:"count"`
}

// Draft is the editable draft of a resource.
type Draft struct {
	Spec       json.RawMessage   `json:"spec"`
	Revision   int64             `json:"revision"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	UpdatedBy  string            `json:"updatedBy,omitempty"`
	Validation *ValidationResult `json:"validation,omitempty"`
	Data       *DataRef          `json:"data,omitempty"`
}

// ResourceRecord is a persisted resource with its draft.
type ResourceRecord struct {
	App           string    `json:"app"`
	Kind          string    `json:"kind"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Draft         Draft     `json:"draft"`
	LatestVersion int64     `json:"latestVersion"`
	LatestHash    string    `json:"latestHash,omitempty"`
	Archived      bool      `json:"archived,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	UpdatedBy     string    `json:"updatedBy,omitempty"`
}

// VersionRecord is an immutable version.
type VersionRecord struct {
	App         string          `json:"app"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Version     int64           `json:"version"`
	Spec        json.RawMessage `json:"spec"`
	Data        *DataRef        `json:"data,omitempty"`
	Message     string          `json:"message,omitempty"`
	ContentHash string          `json:"contentHash"`
	CreatedAt   time.Time       `json:"createdAt"`
	CreatedBy   string          `json:"createdBy"`
	Breaking    *bool           `json:"breaking,omitempty"`
}

// Info returns the API VersionInfo.
func (v VersionRecord) Info() VersionInfo {
	return VersionInfo{Version: v.Version, Message: v.Message, ContentHash: v.ContentHash, CreatedAt: v.CreatedAt, CreatedBy: v.CreatedBy, Breaking: v.Breaking}
}

type VersionInfo struct {
	Version     int64     `json:"version"`
	Message     string    `json:"message,omitempty"`
	ContentHash string    `json:"contentHash"`
	CreatedAt   time.Time `json:"createdAt"`
	CreatedBy   string    `json:"createdBy"`
	Breaking    *bool     `json:"breaking,omitempty"`
}

type ResourceVersion struct {
	Kind string          `json:"kind"`
	Name string          `json:"name"`
	Info VersionInfo     `json:"info"`
	Spec json.RawMessage `json:"spec"`
}

type ResourceStats struct {
	P99Micros      int64   `json:"p99Micros"`
	CallsPerMinute float64 `json:"callsPerMinute"`
	FallbackRate   float64 `json:"fallbackRate"`
}

type ResourceSummary struct {
	Kind          string               `json:"kind"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	Subtype       string               `json:"subtype,omitempty"`
	LatestVersion int64                `json:"latestVersion"`
	DraftChanged  bool                 `json:"draftChanged"`
	Live          map[string]LiveState `json:"live"`
	UpdatedAt     time.Time            `json:"updatedAt"`
	UpdatedBy     string               `json:"updatedBy,omitempty"`
	Stats         *ResourceStats       `json:"stats,omitempty"`
}

type ResourceDraft struct {
	Spec       json.RawMessage   `json:"spec"`
	Revision   int64             `json:"revision"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	UpdatedBy  string            `json:"updatedBy,omitempty"`
	Validation *ValidationResult `json:"validation,omitempty"`
}

type Resource struct {
	Kind          string               `json:"kind"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	Draft         ResourceDraft        `json:"draft"`
	LatestVersion int64                `json:"latestVersion"`
	Live          map[string]LiveState `json:"live"`
}

type ResourceCreate struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Spec        json.RawMessage `json:"spec"`
}

type DraftSave struct {
	Revision    int64           `json:"revision"`
	Description *string         `json:"description,omitempty"`
	Spec        json.RawMessage `json:"spec"`
}

type Usage struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Version     int64  `json:"version,omitempty"`
	UsesVersion int64  `json:"usesVersion,omitempty"`
	Where       string `json:"where,omitempty"`
}

type LookupRow struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// ---------------------------------------------------------------- change requests and releases

const (
	CROpen      = "open"
	CRApproved  = "approved"
	CRRejected  = "rejected"
	CRWithdrawn = "withdrawn"
	CRPublished = "published"
)

type ChangeItemRollout struct {
	Percent int `json:"percent"`
}

type ChangeItem struct {
	Kind        string             `json:"kind"`
	Name        string             `json:"name"`
	Version     int64              `json:"version"`
	Rollout     *ChangeItemRollout `json:"rollout,omitempty"`
	LiveVersion *int64             `json:"liveVersion,omitempty"`
	Diff        string             `json:"diff,omitempty"`
}

type Approval struct {
	By      string    `json:"by"`
	At      time.Time `json:"at"`
	Comment string    `json:"comment,omitempty"`
}

type Comment struct {
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Body string    `json:"body"`
}

type TestCounts struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type ChangeRequest struct {
	ID                  string       `json:"id"`
	App                 string       `json:"-"`
	Environment         string       `json:"environment"`
	Title               string       `json:"title"`
	Description         string       `json:"description,omitempty"`
	Items               []ChangeItem `json:"items"`
	Status              string       `json:"status"`
	Author              string       `json:"author"`
	CreatedAt           time.Time    `json:"createdAt"`
	Approvals           []Approval   `json:"approvals"`
	RequiredApprovals   int          `json:"requiredApprovals"`
	Comments            []Comment    `json:"comments"`
	Tests               *TestCounts  `json:"tests,omitempty"`
	PublishedGeneration *int64       `json:"publishedGeneration,omitempty"`
}

type ChangeRequestCreate struct {
	Environment string       `json:"environment"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Items       []ChangeItem `json:"items"`
}

const (
	ReleaseChangeRequest = "change_request"
	ReleaseData          = "data"
	ReleaseRollout       = "rollout"
	ReleaseRollback      = "rollback"
	ReleaseKillSwitch    = "kill_switch"
)

type ReleaseCreate struct {
	Type            string       `json:"type"`
	ChangeRequestID string       `json:"changeRequestId,omitempty"`
	Items           []ChangeItem `json:"items,omitempty"`
	Decision        string       `json:"decision,omitempty"`
	Percent         *int         `json:"percent,omitempty"`
	ToGeneration    int64        `json:"toGeneration,omitempty"`
	Gate            string       `json:"gate,omitempty"`
	Killed          *bool        `json:"killed,omitempty"`
	Reason          string       `json:"reason,omitempty"`
}

type ReleaseContent struct {
	Kind    string     `json:"kind"`
	Name    string     `json:"name"`
	Version int64      `json:"version"`
	Rollout *LiveState `json:"rollout,omitempty"`
	Changed bool       `json:"changed"`
}

type Release struct {
	Generation       int64            `json:"generation"`
	Environment      string           `json:"environment"`
	Type             string           `json:"type"`
	ChangeRequestID  string           `json:"changeRequestId,omitempty"`
	BreakGlass       bool             `json:"breakGlass"`
	Reason           string           `json:"reason,omitempty"`
	PublishedAt      time.Time        `json:"publishedAt"`
	PublishedBy      string           `json:"publishedBy"`
	ManifestHash     string           `json:"manifestHash"`
	BundleBytes      int64            `json:"bundleBytes"`
	MinEngineVersion string           `json:"minEngineVersion,omitempty"`
	Contents         []ReleaseContent `json:"contents"`
}

// ReleaseRecord is the persisted release: the API view plus what the head
// endpoint and rollbacks need.
type ReleaseRecord struct {
	Release
	App            string   `json:"app"`
	EnvelopeKey    string   `json:"envelopeKey"`
	EnvelopeSHA256 string   `json:"envelopeSha256"`
	BlobHashes     []string `json:"blobHashes"`
	State          EnvState `json:"state"`
}

// ---------------------------------------------------------------- operations

type FleetSummary struct {
	Total                int            `json:"total"`
	OnLatest             int            `json:"onLatest"`
	Generations          map[string]int `json:"generations"`
	Versions             map[string]int `json:"versions"`
	MedianSyncLagSeconds *float64       `json:"medianSyncLagSeconds,omitempty"`
}

type Heartbeat struct {
	ID             string     `json:"id"`
	Node           string     `json:"node,omitempty"`
	SidecarVersion string     `json:"sidecarVersion"`
	EngineVersion  string     `json:"engineVersion"`
	Generation     int64      `json:"generation"`
	LastSyncAt     *time.Time `json:"lastSyncAt,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
	P99Micros      int64      `json:"p99Micros,omitempty"`
	CallsPerMinute float64    `json:"callsPerMinute,omitempty"`
	FallbackRate   float64    `json:"fallbackRate,omitempty"`
}

// SidecarRecord is the persisted fleet entry.
type SidecarRecord struct {
	App         string    `json:"app"`
	Environment string    `json:"environment"`
	Heartbeat   Heartbeat `json:"heartbeat"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
}

type Sidecar struct {
	ID             string     `json:"id"`
	Node           string     `json:"node,omitempty"`
	SidecarVersion string     `json:"sidecarVersion,omitempty"`
	EngineVersion  string     `json:"engineVersion,omitempty"`
	Generation     int64      `json:"generation"`
	LastSyncAt     *time.Time `json:"lastSyncAt,omitempty"`
	LastSeenAt     time.Time  `json:"lastSeenAt"`
	P99Micros      int64      `json:"p99Micros,omitempty"`
	Status         string     `json:"status"`
	LastError      string     `json:"lastError,omitempty"`
}

type LatencyPoint struct {
	At        time.Time `json:"at"`
	P99Micros int64     `json:"p99Micros"`
}

type Overview struct {
	Generation            int64          `json:"generation"`
	P99Micros             int64          `json:"p99Micros"`
	CallsPerMinute        float64        `json:"callsPerMinute"`
	FallbackRate          float64        `json:"fallbackRate"`
	Sidecars              FleetSummary   `json:"sidecars"`
	Decisions             int            `json:"decisions"`
	PendingChangeRequests int            `json:"pendingChangeRequests"`
	LatencySeries         []LatencyPoint `json:"latencySeries"`
	RecentReleases        []Release      `json:"recentReleases"`
}

type AuditEvent struct {
	ID          string         `json:"id"`
	At          time.Time      `json:"at"`
	Actor       string         `json:"actor"`
	Action      string         `json:"action"`
	App         string         `json:"app,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Resource    string         `json:"resource"`
	BreakGlass  bool           `json:"breakGlass,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type WebhookDelivery struct {
	At     time.Time `json:"at"`
	Status int       `json:"status"`
}

type Webhook struct {
	ID           string           `json:"id"`
	URL          string           `json:"url"`
	Events       []string         `json:"events"`
	LastDelivery *WebhookDelivery `json:"lastDelivery,omitempty"`
}

// WebhookRecord is the persisted webhook with its signing secret.
type WebhookRecord struct {
	Webhook
	Secret    string    `json:"secret"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

type WebhookInput struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// WebhookEvents allowed by the contract.
var WebhookEvents = []string{"release.published", "release.rolled_back", "change_request.opened", "change_request.approved", "sidecar.sync_failed"}

// OutboxItem is one pending webhook delivery.
type OutboxItem struct {
	ID         string          `json:"id"`
	WebhookID  string          `json:"webhookId"`
	Event      string          `json:"event"`
	Payload    json.RawMessage `json:"payload"`
	Attempts   int             `json:"attempts"`
	NextAt     time.Time       `json:"nextAt"`
	CreatedAt  time.Time       `json:"createdAt"`
	LeaseUntil time.Time       `json:"leaseUntil,omitempty"`
	LastError  string          `json:"lastError,omitempty"`
}

type BundleHeadEnvelope struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type BundleHeadResource struct {
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
}

type BundleHead struct {
	App         string               `json:"app"`
	Environment string               `json:"environment"`
	Generation  int64                `json:"generation"`
	Envelope    BundleHeadEnvelope   `json:"envelope"`
	Resources   []BundleHeadResource `json:"resources"`
	ExpiresAt   time.Time            `json:"expiresAt"`
}

// HeadPointer is the object-store head file heads/<app>/<env>.json.
type HeadPointer struct {
	Generation int64  `json:"generation"`
	Envelope   string `json:"envelope"`
	SHA256     string `json:"sha256"`
}

type ShadowReport struct {
	Calls             int                  `json:"calls"`
	Agreement         float64              `json:"agreement"`
	Matrix            ShadowMatrix         `json:"matrix"`
	Latency           *ShadowLatency       `json:"latency,omitempty"`
	TimeoutRate       float64              `json:"timeoutRate"`
	SchemaFailureRate float64              `json:"schemaFailureRate"`
	Disagreements     []ShadowDisagreement `json:"disagreements"`
}

type ShadowMatrix struct {
	Labels []string `json:"labels"`
	Rows   [][]int  `json:"rows"`
}

type ShadowLatency struct {
	P50Ms int64 `json:"p50Ms"`
	P95Ms int64 `json:"p95Ms"`
}

type ShadowDisagreement struct {
	RequestID string         `json:"requestId,omitempty"`
	Decided   string         `json:"decided"`
	AI        string         `json:"ai"`
	Vars      map[string]any `json:"vars,omitempty"`
}
