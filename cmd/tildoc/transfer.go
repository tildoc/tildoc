package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tildoc/tildoc/internal/config"
	"github.com/tildoc/tildoc/internal/core"
)

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

// exportCmd: tildoc export -user NAME (-space ID|SLUG | -folder ID | -collection ID) [-format zip|bundle] -o FILE
func exportCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	user := fs.String("user", "", "act as this user (permissions apply)")
	space := fs.String("space", "", "space id or short name")
	folder := fs.String("folder", "", "folder id")
	coll := fs.String("collection", "", "collection id")
	format := fs.String("format", "zip", "zip | bundle")
	out := fs.String("o", "", "output file")
	fs.Parse(args)
	if *user == "" || *out == "" {
		return errors.New("-user and -o are required")
	}
	svc, err := open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer svc.DB.Close()
	p, err := svc.PrincipalByUsername(ctx, *user)
	if err != nil {
		return err
	}
	var scope core.ExportScope
	switch {
	case *space != "":
		sp, err := svc.GetSpace(ctx, p, *space)
		if err != nil {
			return err
		}
		scope = core.ExportScope{Kind: "space", ID: sp.ID}
	case *folder != "":
		scope = core.ExportScope{Kind: "folder", ID: *folder}
	case *coll != "":
		scope = core.ExportScope{Kind: "collection", ID: *coll}
	default:
		return errors.New("one of -space, -folder, -collection is required")
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if *format == "bundle" {
		err = svc.ExportBundle(ctx, p, scope, f, nil)
	} else {
		err = svc.ExportZip(ctx, p, scope, f, nil)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(*out)
		return err
	}
	fmt.Println("exported to", *out)
	return nil
}

// importCmd: tildoc import -user NAME [-space-name N] [-space-slug S] [-team ID] [-folder ID] FILE
func importCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	user := fs.String("user", "", "import as this user")
	name := fs.String("space-name", "", "name of the new space (default: from the bundle)")
	slug := fs.String("space-slug", "", "short name of the new space")
	team := fs.String("team", "", "team id owning the new space")
	folder := fs.String("folder", "", "import into this folder instead of a new space")
	fs.Parse(args)
	if *user == "" || fs.NArg() != 1 {
		return errors.New("usage: tildoc import -user NAME [options] FILE")
	}
	svc, err := open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer svc.DB.Close()
	p, err := svc.PrincipalByUsername(ctx, *user)
	if err != nil {
		return err
	}
	file := fs.Arg(0)
	if strings.EqualFold(filepath.Ext(file), ".zip") {
		if *folder == "" {
			return errors.New("a ZIP is imported with -folder")
		}
		res, err := svc.ImportZip(ctx, p, *folder, file, nil)
		if err != nil {
			return err
		}
		printJSON(res)
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	res, err := svc.ImportBundle(ctx, p, f, nil, core.ImportOptions{NewSpaceName: *name, NewSpaceSlug: *slug, TeamID: *team, TargetFolderID: *folder}, nil)
	if err != nil {
		return err
	}
	printJSON(res)
	return nil
}

// backupCmd: tildoc backup full|differential|list|verify FILE [FILE]
func backupCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: tildoc backup full|differential|list|verify FILE [FILE]")
	}
	if args[0] == "verify" {
		sum, err := core.VerifyBackup(args[1:])
		if err != nil {
			return err
		}
		printJSON(sum)
		return nil
	}
	svc, err := open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer svc.DB.Close()
	switch args[0] {
	case "full", "differential":
		b, err := svc.BackupNow(ctx, args[0])
		if err != nil {
			return err
		}
		if b.Status != "done" {
			return fmt.Errorf("backup %s: %s", b.Status, b.Error)
		}
		fmt.Println(filepath.Join(cfg.BackupDir, b.File))
	case "list":
		list, err := svc.ListBackups(ctx, core.SystemPrincipal)
		if err != nil {
			return err
		}
		for _, b := range list {
			base := ""
			if b.BaseID != nil {
				base = "base " + (*b.BaseID)[:8]
			}
			fmt.Printf("%s  %-12s %-6s %10d bytes  %s %s\n", b.StartedAt.Format("2006-01-02 15:04"), b.Mode, b.Status, b.Size, b.File, base)
		}
	default:
		return errors.New("usage: tildoc backup full|differential|list|verify FILE [FILE]")
	}
	return nil
}

// restoreCmd: tildoc restore FULL [DIFFERENTIAL] (empty instance only)
func restoreCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: tildoc restore FULL-BACKUP [DIFFERENTIAL-BACKUP]")
	}
	svc, err := open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer svc.DB.Close()
	sum, err := svc.RestoreServer(ctx, args)
	if err != nil {
		return err
	}
	printJSON(sum)
	fmt.Fprintln(os.Stderr, "restored. Access keys work only with the same TILDOC_MASTER_KEY as the backed up server.")
	return nil
}
