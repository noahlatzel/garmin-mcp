// Command garmin is a single-user Garmin Connect MCP service. It writes native
// strength and pool-swim workouts (named exercises, reps, weight, strokes,
// drills, pace targets) to Garmin Connect and serves the Garmin wellness reads
// (daily summary, sleep, HRV, stress, body battery). One deployment serves one
// linked Garmin account.
//
// Modes:
//
//	garmin login   -- one-time credentialed account link.
//	                  Reads GARMIN_EMAIL/GARMIN_PASSWORD, prompts for MFA on
//	                  stdin, and persists OAuth tokens to GARMIN_TOKEN_PATH.
//	garmin verify  -- on-box live round-trip of the workout/calendar/wellness
//	                  tools against the linked account (creates a throwaway
//	                  far-future workout and deletes it). Uses the token store
//	                  directly, not the MCP server. Run by hand to confirm the
//	                  api.go paths still answer live.
//	garmin verify swim -- like verify, but round-trips the pool-swim builder's
//	                  enums (stroke/drill/equipment/pace/pool length).
//	garmin         -- serve the MCP server. Streamable HTTP on GARMIN_LISTEN_ADDR
//	                  if set (gated by MCP_BEARER_TOKEN), else stdio.
//
// Environment:
//
//	GARMIN_TOKEN_PATH    -- file holding persisted OAuth tokens (0600). Required.
//	GARMIN_LISTEN_ADDR   -- e.g. 127.0.0.1:8082; if unset, serve over stdio.
//	MCP_BEARER_TOKEN     -- HTTP mode: the shared bearer token every request must
//	                        present.
//	GARMIN_EMAIL/_PASSWORD -- only read by `garmin login`; never persisted.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/goosegit97/garmin-mcp/internal/config"
	"github.com/goosegit97/garmin-mcp/internal/garmin"
	mcputil "github.com/goosegit97/garmin-mcp/internal/mcp"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	config.LoadDotEnv()

	if len(os.Args) > 1 && os.Args[1] == "login" {
		if err := runLogin(); err != nil {
			slog.Error("garmin: login failed", "err", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "verify" {
		mode := ""
		if len(os.Args) > 2 {
			mode = os.Args[2]
		}
		if err := runVerify(mode); err != nil {
			slog.Error("garmin: verify failed", "err", err)
			os.Exit(1)
		}
		return
	}

	if err := runServe(); err != nil {
		slog.Error("garmin: server error", "err", err)
		os.Exit(1)
	}
}

// tokenPath returns the required token-store path.
func tokenPath() (string, error) {
	return config.RequireEnv("GARMIN_TOKEN_PATH")
}

// runVerify drives the new connectapi tools end-to-end against the real account
// behind the token store, on a throwaway far-future workout it then deletes. It
// is the turnkey on-box check that the api.go paths answer live (no MCP/JWT). It
// makes real API calls, so it is run by hand on the box, never by the server.
func runVerify(mode string) error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	svc, err := garmin.NewService(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch mode {
	case "":
		return svc.VerifyLive(ctx, os.Stdout)
	case "swim":
		return svc.VerifySwimLive(ctx, os.Stdout)
	default:
		return fmt.Errorf("unknown verify mode %q (want empty or swim)", mode)
	}
}

// runLogin performs the one-time credentialed account link. It deliberately runs
// exactly one login attempt to avoid tripping Garmin's fresh-login rate limiting.
func runLogin() error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	email, err := config.RequireEnv("GARMIN_EMAIL")
	if err != nil {
		return err
	}
	password, err := config.RequireEnv("GARMIN_PASSWORD")
	if err != nil {
		return err
	}

	svc, err := garmin.NewService(path)
	if err != nil {
		return err
	}

	creds := garmin.Credentials{
		Username: email,
		Password: password,
		MFA:      promptMFA,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := svc.Bootstrap(ctx, creds); err != nil {
		return err
	}
	slog.Info("garmin: account linked", "token_path", path)
	return nil
}

// promptMFA reads a TOTP code from stdin — the code shown by the operator's
// authenticator app.
func promptMFA(ctx context.Context) (string, error) {
	fmt.Fprint(os.Stderr, "Enter Garmin MFA code: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading MFA code: %w", err)
	}
	code := strings.TrimSpace(line)
	if code == "" {
		return "", errors.New("no MFA code entered")
	}
	return code, nil
}

// runServe starts the MCP server over HTTP (if GARMIN_LISTEN_ADDR is set) or
// stdio. It does not log in: it relies on tokens persisted by `garmin login`.
func runServe() error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	svc, err := garmin.NewService(path)
	if err != nil {
		return err
	}
	server := garmin.NewMCPServer(svc)

	addr := strings.TrimSpace(os.Getenv("GARMIN_LISTEN_ADDR"))
	if addr == "" {
		err := server.Run(context.Background(), &mcp.StdioTransport{})
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}

	token := strings.TrimSpace(os.Getenv("MCP_BEARER_TOKEN"))
	if token == "" {
		return errors.New("MCP_BEARER_TOKEN must be set when serving HTTP")
	}
	return mcputil.ServeResourceServer(mcputil.ResourceServerConfig{
		Addr:    addr,
		Service: "garmin",
		Auth:    mcputil.StaticTokenMiddleware(token),
	}, server)
}
