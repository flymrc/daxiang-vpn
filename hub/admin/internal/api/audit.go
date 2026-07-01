package api

import (
	"context"
	"log"
	"time"

	"zongheng-vpn/hub/internal/auth"
)

func (s *Server) audit(eventType string, actor string, sourceIP string, target string, detailJSON string, result string, errorCode string) {
	if err := s.store.InsertAudit(context.Background(), auth.AuditEvent{
		OccurredAt: time.Now(),
		Actor:      actor,
		SourceIP:   sourceIP,
		EventType:  eventType,
		Target:     target,
		DetailJSON: detailJSON,
		Result:     result,
		ErrorCode:  errorCode,
	}); err != nil {
		log.Printf("admin audit 写入失败: %v", err)
	}
}
