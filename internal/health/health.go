package health

// Status describes the health state of a subsystem.
type Status struct {
	Status  string         `json:"status"` // running, connected, stopped, error
	Details map[string]any `json:"details,omitempty"`
}

// Statuser is implemented by subsystems that can report health.
type Statuser interface {
	HealthStatus() Status
}
