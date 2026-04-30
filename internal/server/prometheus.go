package server

import (
	"net/http"

	"github.com/prometheus/common/expfmt"
)

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	if s.promRegistry == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	gathered, err := s.promRegistry.Gather()
	if err != nil {
		s.logger.Error().Err(err).Msg("failed to gather prometheus metrics")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", string(expfmt.NewFormat(expfmt.TypeTextPlain)))
	for _, mf := range gathered {
		if _, err := expfmt.MetricFamilyToText(w, mf); err != nil {
			s.logger.Error().Err(err).Str("metric", mf.GetName()).Msg("failed to write metric")
			return
		}
	}
}
