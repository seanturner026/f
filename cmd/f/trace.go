package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/seanturner026/pi/internal/tracing"
)

const (
	jaegerName = "f-jaeger"
	jaegerUI   = "http://localhost:16686"
	jaegerOTLP = "http://localhost:4318/v1/traces"
)

// traceOpen is `f trace`: start Jaeger (Docker) if needed, send it the traces
// since since (it keeps them in memory only, so every run re-sends), open the UI.
func traceOpen(ctx context.Context, d deps, since time.Duration) error {
	lines, err := tracing.Lines(tracing.Path(), time.Now().Add(-since))
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return fmt.Errorf("no traces in the last %s (%s)", since, tracing.Path())
	}
	if id, _ := d.run.Run(ctx, "", "docker", "ps", "-q", "--filter", "name=^"+jaegerName+"$"); id == "" {
		info("starting Jaeger (docker: %s)", jaegerName)
		if _, err := d.run.Run(ctx, "", "docker", "run", "-d", "--rm", "--name", jaegerName,
			"-e", "COLLECTOR_OTLP_ENABLED=true", "-p", "16686:16686", "-p", "4318:4318",
			"jaegertracing/all-in-one"); err != nil {
			return err
		}
	}
	if err := waitForJaeger(ctx); err != nil {
		return err
	}
	for _, l := range lines {
		if err := postTrace(ctx, l); err != nil {
			return err
		}
	}
	ok("sent %d trace lines (last %s) to Jaeger: %s", len(lines), since, jaegerUI)
	_, err = d.run.Run(ctx, "", "open", jaegerUI)
	return err
}

// waitForJaeger polls the UI and the OTLP endpoint until both answer (30s).
func waitForJaeger(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		ui, err1 := http.Get(jaegerUI)
		otlp, err2 := http.Post(jaegerOTLP, "application/json", bytes.NewReader([]byte("{}")))
		for _, r := range []*http.Response{ui, otlp} {
			if r != nil {
				r.Body.Close()
			}
		}
		if err1 == nil && err2 == nil && ui.StatusCode == http.StatusOK && otlp.StatusCode < 500 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("Jaeger didn't come up within 30s (docker logs " + jaegerName + ")")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func postTrace(ctx context.Context, line []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, jaegerOTLP, bytes.NewReader(line))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("jaeger: %s", resp.Status)
	}
	return nil
}

// traceStop is `f trace stop`.
func traceStop(ctx context.Context, d deps) error {
	_, err := d.run.Run(ctx, "", "docker", "stop", jaegerName)
	return err
}

// traceStats is `f trace stats`: no Docker, text for people and agents.
func traceStats(since time.Duration) error {
	return tracing.Stats(os.Stdout, tracing.Path(), time.Now().Add(-since))
}
