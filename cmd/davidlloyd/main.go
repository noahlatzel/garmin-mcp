// Command davidlloyd syncs body weight from a David Lloyd gym account into Garmin
// Connect, from where it flows on to Intervals.icu via the existing Garmin sync.
//
// It is headless by design: a weight sync is a deterministic periodic job, not an
// interactive capability, so there is no MCP server and no LLM in the loop. It
// runs as a one-shot under a systemd timer.
//
// It never stores the password. `davidlloyd login` reads the credentials once,
// exchanges them for a session token, and persists only that token; `davidlloyd
// sync` uses the stored token (and the garmin token store) and re-prompts for a
// fresh login only if the session has expired.
//
// Modes:
//
//	davidlloyd login   -- one-time bootstrap. Reads DL_USERNAME/DL_PASSWORD
//	                      (+ optional DL_BRAND), logs in, and saves ONLY the
//	                      session token to DAVIDLLOYD_SESSION_PATH.
//	davidlloyd verify  -- read-only: print the latest weight + full body panel.
//	davidlloyd sync    -- read the latest weight and write it to Garmin Connect
//	                      (weigh-in) via the garmin token store. The periodic job.
//
// Environment:
//
//	DAVIDLLOYD_SESSION_PATH -- file holding the persisted session token (0600). Required.
//	GARMIN_TOKEN_PATH       -- garmin token store, read by `sync` to write the weigh-in. Required for sync.
//	DL_USERNAME/DL_PASSWORD -- only read by `davidlloyd login`; never persisted.
//	DL_BRAND                -- optional Netpulse brand slug; defaults to "davidlloydclubs".
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/goosegit97/garmin-mcp/internal/config"
	"github.com/goosegit97/garmin-mcp/internal/davidlloyd"
	"github.com/goosegit97/garmin-mcp/internal/garmin"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	config.LoadDotEnv()

	mode := ""
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	var err error
	switch mode {
	case "login":
		err = runLogin()
	case "verify":
		err = runVerify()
	case "sync":
		err = runSync()
	default:
		fmt.Fprintln(os.Stderr, "usage: davidlloyd <login|sync|verify>")
		os.Exit(2)
	}
	if err != nil {
		slog.Error("davidlloyd: "+mode+" failed", "err", err)
		os.Exit(1)
	}
}

func sessionPath() (string, error) { return config.RequireEnv("DAVIDLLOYD_SESSION_PATH") }

// runLogin performs the one-time credentialed bootstrap, persisting only the
// session token. Credentials are read transiently and never stored.
func runLogin() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	username, err := config.RequireEnv("DL_USERNAME")
	if err != nil {
		return err
	}
	password, err := config.RequireEnv("DL_PASSWORD")
	if err != nil {
		return err
	}
	brand := strings.TrimSpace(os.Getenv("DL_BRAND"))

	svc := davidlloyd.NewService(path)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := svc.Bootstrap(ctx, brand, username, password); err != nil {
		return err
	}
	slog.Info("davidlloyd: linked", "session_path", path)
	return nil
}

// runVerify prints the latest weight + full body panel (read-only).
func runVerify() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	svc := davidlloyd.NewService(path)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	weight, err := svc.LatestWeight(ctx)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	fmt.Fprintln(os.Stdout, "latest weight:")
	if err := enc.Encode(weight); err != nil {
		return err
	}
	body, err := svc.LatestBody(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "\nfull body panel (%d metrics):\n", len(body))
	return enc.Encode(body)
}

// runSync reads the latest David Lloyd weight and writes it to Garmin Connect as
// a weigh-in on the measurement date. This is the periodic job. It composes the
// davidlloyd (read) and garmin (write) libraries; neither running service depends
// on the other.
func runSync() error {
	dlPath, err := sessionPath()
	if err != nil {
		return err
	}
	garminTokenPath, err := config.RequireEnv("GARMIN_TOKEN_PATH")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 1. Read the latest scale weight from David Lloyd.
	dl := davidlloyd.NewService(dlPath)
	weight, err := dl.LatestWeight(ctx)
	if err != nil {
		return fmt.Errorf("reading David Lloyd weight: %w", err)
	}

	// 2. Garmin only accepts a calendar date; a raw non-RFC 3339 string must
	// fail here rather than be passed through as a bogus date.
	t, err := time.Parse(time.RFC3339, weight.MeasuredAt)
	if err != nil {
		return fmt.Errorf("David Lloyd measurement timestamp %q is not RFC 3339; refusing to sync: %w",
			weight.MeasuredAt, err)
	}
	date := t.Format("2006-01-02")

	// 3. Skip the write when this exact measurement was already synced (the job
	// runs daily; the scale is used less often than that).
	store := davidlloyd.NewSessionStore(dlPath)
	sess, err := store.Load()
	if err != nil {
		return err
	}
	if sess != nil && sess.LastSyncedMeasuredAt == weight.MeasuredAt && sess.LastSyncedKg == weight.Kg {
		slog.Info("davidlloyd: measurement already synced; nothing to do",
			"kg", weight.Kg, "measured_at", weight.MeasuredAt)
		return nil
	}

	// 4. Write the weigh-in to Garmin on the measurement date.
	gm, err := garmin.NewService(garminTokenPath)
	if err != nil {
		return err
	}
	if _, err := gm.SetWeight(ctx, weight.Kg, date); err != nil {
		return fmt.Errorf("writing weigh-in to Garmin: %w", err)
	}
	slog.Info("davidlloyd: synced weigh-in to Garmin",
		"kg", weight.Kg, "measured_at", weight.MeasuredAt, "date", date)

	// 5. Record what was synced; a failure here only costs a redundant
	// re-write tomorrow, so it must not fail the sync.
	if sess != nil {
		sess.LastSyncedMeasuredAt = weight.MeasuredAt
		sess.LastSyncedKg = weight.Kg
		if err := store.Save(sess); err != nil {
			slog.Warn("davidlloyd: could not record last-synced measurement", "err", err)
		}
	}
	return nil
}
