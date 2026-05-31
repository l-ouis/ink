// Command ink is a single-binary personal website. Content lives as Markdown
// files on disk, each served at a path you choose; the owner edits from the web.
package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"ink/internal/auth"
	"ink/internal/canvas"
	"ink/internal/config"
	"ink/internal/media"
	"ink/internal/server"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

func main() {
	log.SetFlags(0)
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "passwd":
		err = runPasswd(args)
	default:
		fmt.Fprintf(os.Stderr, "ink: unknown command %q\nusage: ink [serve|passwd] [flags]\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("ink: %v", err)
	}
}

func runServe(args []string) error {
	fset := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fset.String("addr", ":8080", "listen address")
	contentDir := fset.String("content", "content", "content directory (uploads live here)")
	canvasPath := fset.String("canvas", "data/canvas.json", "canvas data file path")
	configPath := fset.String("config", "data/config.json", "config file path")
	if err := fset.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if _, err := cfg.EnsureSecret(*configPath); err != nil {
		return fmt.Errorf("init secret: %w", err)
	}
	if !cfg.HasPassword() {
		log.Printf("warning: no password set. Run `ink passwd` to enable sign-in.")
	}

	cv, err := canvas.New(*canvasPath)
	if err != nil {
		return fmt.Errorf("open canvas: %w", err)
	}

	mediaStore, err := media.New(filepath.Join(*contentDir, "uploads"))
	if err != nil {
		return fmt.Errorf("open media store: %w", err)
	}

	am := auth.New(cfg.Secret(), cfg.SecureCookies)

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}
	srv, err := server.New(cfg, *configPath, cv, mediaStore, am, templatesFS, staticSub)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("ink listening on %s  (content=%s)", *addr, *contentDir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("ink: serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(ctx)
}

func runPasswd(args []string) error {
	fset := flag.NewFlagSet("passwd", flag.ExitOnError)
	configPath := fset.String("config", "data/config.json", "config file path")
	if err := fset.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	pw, err := readNewPassword()
	if err != nil {
		return err
	}
	if strings.TrimSpace(pw) == "" {
		return errors.New("password must not be empty")
	}
	if err := cfg.SetPassword(pw); err != nil {
		return err
	}
	if err := cfg.Save(*configPath); err != nil {
		return err
	}
	fmt.Println("Password updated.")
	return nil
}

func readNewPassword() (string, error) {
	fd := int(syscall.Stdin)
	if !term.IsTerminal(fd) {
		// Non-interactive (e.g. piped input): read a single line.
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			return "", errors.New("no password on stdin")
		}
		return sc.Text(), sc.Err()
	}
	fmt.Print("New password: ")
	p1, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	fmt.Print("Confirm password: ")
	p2, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("passwords do not match")
	}
	return string(p1), nil
}
