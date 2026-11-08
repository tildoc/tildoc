// Command tildoc is the tildoc server, worker and admin CLI.
//
//	tildoc serve    REST API + web UI + WebDAV (HTTP), S3 gateway, SFTP gateway
//	tildoc worker   background processing (text extraction, OCR, maintenance)
//	tildoc all      serve + worker in one process
//	tildoc migrate  apply database migrations
//	tildoc user create|passwd ...
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tildoc/tildoc/internal/api"
	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/config"
	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/dav"
	"github.com/tildoc/tildoc/internal/db"
	"github.com/tildoc/tildoc/internal/s3"
	"github.com/tildoc/tildoc/internal/sftpd"
	"github.com/tildoc/tildoc/internal/vfs"
	"github.com/tildoc/tildoc/internal/worker"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: tildoc <command>

commands:
  serve                 run HTTP (API, UI, WebDAV), S3 and SFTP servers
  worker                run the background worker
  all                   serve + worker in one process
  migrate               apply database migrations
  user create -username NAME [-admin] [-display NAME]   (password from stdin)
  user passwd -username NAME                             (password from stdin)

configuration: TILDOC_* environment variables, see deploy/.env.example`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:\n"+err.Error())
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "serve":
		err = run(ctx, cfg, log, true, false)
	case "worker":
		err = run(ctx, cfg, log, false, true)
	case "all":
		err = run(ctx, cfg, log, true, true)
	case "migrate":
		var svc *core.Service
		if svc, err = open(ctx, cfg, log); err == nil {
			svc.DB.Close()
			log.Info("migrations applied")
		}
	case "user":
		err = userCmd(ctx, cfg, log, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func open(ctx context.Context, cfg *config.Config, log *slog.Logger) (*core.Service, error) {
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	blobs, err := blob.NewLocal(filepath.Join(cfg.DataDir, "store"))
	if err != nil {
		pool.Close()
		return nil, err
	}
	return core.New(pool, blobs, cfg, log), nil
}

func run(ctx context.Context, cfg *config.Config, log *slog.Logger, serve, work bool) error {
	svc, err := open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer svc.DB.Close()
	errc := make(chan error, 4)

	if work {
		w := worker.New(svc, log)
		go func() { errc <- w.Run(ctx) }()
	}
	var shutdown []func(context.Context) error
	if serve {
		fsys := vfs.New(svc)
		apiSrv := api.New(svc, cfg, log)
		apiSrv.Mounts[dav.Prefix+"/"] = dav.New(fsys, log, cfg.TrustedProxies)
		httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: apiSrv.Handler(), ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute}
		s3Srv := &http.Server{Addr: cfg.S3Addr, Handler: s3.New(svc, fsys, log, cfg.S3Region, cfg.TrustedProxies),
			ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute}
		for _, hs := range []*http.Server{httpSrv, s3Srv} {
			hs := hs
			go func() {
				log.Info("listening", "addr", hs.Addr)
				if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errc <- err
				}
			}()
			shutdown = append(shutdown, hs.Shutdown)
		}
		if cfg.SFTPAddr != "" && cfg.SFTPAddr != "off" {
			key, err := sftpd.LoadOrCreateHostKey(filepath.Join(cfg.DataDir, "ssh_host_ed25519_key"))
			if err != nil {
				return err
			}
			l, err := net.Listen("tcp", cfg.SFTPAddr)
			if err != nil {
				return err
			}
			sf := sftpd.New(fsys, log, key)
			log.Info("listening (sftp)", "addr", cfg.SFTPAddr)
			go func() { errc <- sf.Serve(l) }()
			shutdown = append(shutdown, func(context.Context) error { return sf.Close() })
		}
	}
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, f := range shutdown {
		f(sctx)
	}
	return err
}

func readPassword() (string, error) {
	fmt.Fprint(os.Stderr, "password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func userCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	if len(args) < 1 {
		usage()
	}
	fs := flag.NewFlagSet("user", flag.ExitOnError)
	username := fs.String("username", "", "username")
	admin := fs.Bool("admin", false, "grant admin rights")
	display := fs.String("display", "", "display name")
	fs.Parse(args[1:])
	if *username == "" {
		return errors.New("-username required")
	}
	svc, err := open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer svc.DB.Close()
	pw, err := readPassword()
	if err != nil {
		return err
	}
	switch args[0] {
	case "create":
		u, err := svc.CreateUser(ctx, core.SystemPrincipal, *username, pw, *display, *admin)
		if err != nil {
			return err
		}
		fmt.Println("created user", u.Username, u.ID)
	case "passwd":
		u, err := svc.UserByName(ctx, *username)
		if err != nil {
			return err
		}
		adm := &core.Principal{UserID: core.SystemPrincipal.UserID, IsAdmin: true, Via: "system"}
		if _, err := svc.UpdateUser(ctx, adm, u.ID, core.UserUpdate{Password: &pw}); err != nil {
			return err
		}
		fmt.Println("password updated for", u.Username)
	default:
		usage()
	}
	return nil
}
