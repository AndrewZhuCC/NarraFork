package agentbasic

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

const defaultMaxSSEEventBytes = 1024 * 1024

type sseEvent struct {
	Event string
	Data  string
}

func readSSE(ctx context.Context, reader io.Reader, maxEventBytes int, emit func(sseEvent) error) error {
	if maxEventBytes <= 0 {
		maxEventBytes = defaultMaxSSEEventBytes
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventBytes)
	eventType := ""
	dataLines := []string{}
	dispatch := func() error {
		if len(dataLines) == 0 && strings.TrimSpace(eventType) == "" {
			return nil
		}
		event := sseEvent{Event: eventType, Data: strings.Join(dataLines, "\n")}
		eventType = ""
		dataLines = nil
		return emit(event)
	}
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			field = line
			value = ""
		} else if strings.HasPrefix(value, " ") {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			eventType = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read sse: %w", err)
	}
	return dispatch()
}
