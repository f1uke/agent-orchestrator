package releasetrain

// StageKey names one of the six release stages.
type StageKey string

// The six release stages in order.
const (
	StageFreeze  StageKey = "freeze"
	StageScan    StageKey = "scan"
	StageApprove StageKey = "approve"
	StageTag     StageKey = "tag"
	StagePublish StageKey = "publish"
	StageForce   StageKey = "force"
)

// StageStatus is where a stage stands.
type StageStatus string

// Stage statuses.
const (
	StatusDone          StageStatus = "done"
	StatusRunning       StageStatus = "running"
	StatusWaiting       StageStatus = "waiting"
	StatusFailed        StageStatus = "failed"
	StatusLate          StageStatus = "late"
	StatusTodo          StageStatus = "todo"
	StatusUnknown       StageStatus = "unknown"
	StatusNotObservable StageStatus = "not_observable"
)

// Stage is one stage row of one app version.
type Stage struct {
	Key          StageKey         `json:"key"`
	Date         *Day             `json:"date,omitempty"`
	Status       StageStatus      `json:"status"`
	Detail       string           `json:"detail"`
	Conventional bool             `json:"conventional,omitempty"`
	Reads        []KindName       `json:"reads"`
	Missing      []ResourceStatus `json:"missing,omitempty"`
}
