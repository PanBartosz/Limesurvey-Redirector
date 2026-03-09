package models

import "time"

type RPCTransport string

const (
	RPCTransportXML  RPCTransport = "xmlrpc"
	RPCTransportJSON RPCTransport = "jsonrpc"
)

type QueryForwardMode string

const (
	QueryForwardAll  QueryForwardMode = "all"
	QueryForwardNone QueryForwardMode = "none"
)

type StickinessMode string

const (
	StickinessOff      StickinessMode = ""
	StickinessCookie   StickinessMode = "cookie"
	StickinessQueryKey StickinessMode = "query_param"
)

type Instance struct {
	ID               int64
	Name             string
	SurveyBaseURL    string
	RemoteControlURL string
	RPCTransport     RPCTransport
	Username         string
	SecretRef        string
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Route struct {
	ID                int64
	Slug              string
	Name              string
	Description       string
	OwnerUsername     string
	OwnerRole         string
	Algorithm         string
	FuzzyThreshold    int
	PendingEnabled    bool
	PendingTTLSeconds int
	PendingWeight     float64
	ForwardQueryMode  QueryForwardMode
	FallbackURL       string
	Enabled           bool
	StickinessEnabled bool
	StickinessMode    StickinessMode
	StickinessParam   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Targets           []RouteTarget
}

type RouteTarget struct {
	ID          int64
	RouteID     int64
	InstanceID  int64
	SurveyID    int64
	DisplayName string
	Weight      int
	HardCap     *int
	Priority    int
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Instance    Instance
	Stats       *TargetStats
}

type InstanceSummary struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	RPCTransport string `json:"rpc_transport"`
}

type TargetStats struct {
	RouteTargetID       int64
	CompletedResponses  int
	IncompleteResponses int
	FullResponses       int
	SurveyActive        bool
	FetchError          string
	FetchedAt           time.Time
}

type RedirectDecision struct {
	ID                int64
	RouteID           int64
	RouteTargetID     *int64
	RequestID         string
	DecisionMode      string
	RequestQuery      string
	ForwardedQuery    string
	CandidateSnapshot string
	ChosenScore       *float64
	Status            string
	CreatedAt         time.Time
}

type User struct {
	ID             int64
	Username       string
	Role           string
	Enabled        bool
	SessionVersion int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
