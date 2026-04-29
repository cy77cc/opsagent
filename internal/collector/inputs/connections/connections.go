package connections

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("connections", func() collector.Input {
		return &ConnectionsInput{}
	})
}

// ConnectionsInput gathers network connection statistics.
type ConnectionsInput struct {
	states []string
}

func (c *ConnectionsInput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["states"]; ok {
		switch states := v.(type) {
		case []interface{}:
			for _, item := range states {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("connections: states items must be strings, got %T", item)
				}
				c.states = append(c.states, s)
			}
		case []string:
			c.states = states
		default:
			return fmt.Errorf("connections: states must be a list, got %T", v)
		}
	}
	return nil
}

func (c *ConnectionsInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	conns, err := net.ConnectionsWithContext(ctx, "all")
	if err != nil {
		if os.IsPermission(err) {
			log.Info().Str("plugin", "connections").Msg("permission denied, skipping")
			return nil
		}
		return fmt.Errorf("connections: failed to get connections: %w", err)
	}

	// Count by state and protocol
	counts := make(map[string]map[string]int)
	for _, conn := range conns {
		state := conn.Status
		protocol := "tcp"
		if conn.Type == 2 { // SOCK_DGRAM
			protocol = "udp"
		}

		if counts[protocol] == nil {
			counts[protocol] = make(map[string]int)
		}
		counts[protocol][state]++
	}

	// Filter by configured states if any
	for protocol, stateCounts := range counts {
		for state, count := range stateCounts {
			if len(c.states) > 0 {
				found := false
				for _, s := range c.states {
					if s == state {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			tags := map[string]string{
				"state":    state,
				"protocol": protocol,
			}
			fields := map[string]interface{}{
				"count_by_state": int64(count),
			}
			acc.AddGauge("connections", tags, fields)
		}
	}

	return nil
}

func (c *ConnectionsInput) SampleConfig() string {
	return `
  ## List of connection states to filter. Empty means all states.
  # states = ["ESTABLISHED", "LISTEN", "TIME_WAIT"]
`
}
