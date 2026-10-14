// Package metrics preserves upstream command instrumentation as inert hooks.
// Beads Continuity does not collect or upload anonymous usage metrics.
package metrics

const (
	EnvDisableMetrics     = "BD_DISABLE_METRICS"
	EnvDisableEventFlush  = "BD_DISABLE_EVENT_FLUSH"
	SendMetricsSubcommand = "send-metrics"
)

// Event and Collector are compatibility types; no event data is retained.
type Event struct{}
type Collector struct{}

func NewCommandEvent(string) *Event        { return nil }
func Global() *Collector                   { return nil }
func (*Collector) CloseEventAndAdd(*Event) {}
func CloseAndFlush()                       {}
func RunSendMetrics() int                  { return 0 }
