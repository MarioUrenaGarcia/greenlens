// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
)

func waitCalibration(t *testing.T, c call) api.CalibrationJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, body := c.do("GET", api.PathCalibRun, nil)
		var job api.CalibrationJob
		if resp.code != 200 || json.Unmarshal(body, &job) != nil {
			t.Fatalf("calibration: %d %s", resp.code, body)
		}
		if job.State != "running" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("still running: %+v", job)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCalibrationInTheAgent(t *testing.T) {
	old := minCalibration
	minCalibration = 100 * time.Millisecond
	t.Cleanup(func() { minCalibration = old })
	r := startAgent(t, "")
	waitSamples(t, r, 2)
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	for _, bad := range []float64{0.05, 601, -1} {
		if resp, _ := sock.do("POST", api.PathCalibRun, api.CalibrationRequest{DurationS: bad}); resp.code != http.StatusBadRequest {
			t.Errorf("duration %v accepted: %d", bad, resp.code)
		}
	}
	if resp, body := sock.do("POST", api.PathCalibRun, api.CalibrationRequest{DurationS: 0.5}); resp.code != http.StatusAccepted {
		t.Fatalf("start: %d %s", resp.code, body)
	}
	if resp, _ := sock.do("POST", api.PathCalibRun, api.CalibrationRequest{DurationS: 0.5}); resp.code != http.StatusConflict {
		t.Errorf("a second calibration started: %d", resp.code)
	}
	if resp, _ := sock.do("POST", api.PathBenchmarks, api.BenchmarkRequest{Kind: "ollama", Model: "m", Runs: 2, MaxTokens: 4}); resp.code != http.StatusConflict {
		t.Errorf("a benchmark started during a calibration: %d", resp.code)
	}
	job := waitCalibration(t, sock)
	if job.State != "done" || job.Result == nil || job.Result.Samples == 0 || job.Result.Domains["package"].P50 <= 0 {
		t.Fatalf("job %+v", job)
	}
	st, err := r.client.Status(t.Context())
	if err != nil || st.Calibration == nil || st.Calibration.Baseline.CPUW != job.Result.Domains["package"].P50 {
		t.Fatalf("calibration not applied: %+v %v", st.Calibration, err)
	}
	minCalibration = 10 * time.Second
	if resp, _ := sock.do("POST", api.PathCalibRun, api.CalibrationRequest{DurationS: 5}); resp.code != http.StatusBadRequest {
		t.Fatal("short calibration accepted")
	}
	if resp, _ := sock.do("POST", api.PathCalibRun, api.CalibrationRequest{DurationS: 30}); resp.code != http.StatusAccepted {
		t.Fatal("long calibration refused")
	}
	if resp, _ := sock.do("DELETE", api.PathCalibRun, nil); resp.code != http.StatusAccepted {
		t.Fatal("cancel refused")
	}
	if job := waitCalibration(t, sock); job.State != "canceled" {
		t.Fatalf("canceled job %+v", job)
	}
}
