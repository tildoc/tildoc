// Package vfs exposes tildoc as a path based virtual filesystem:
//
//	/                         all spaces the caller can read
//	/<space-slug>             root folder of a space
//	/<space-slug>/a/b/x.pdf   folders and files (links)
//	/@collections/<name>/x.pdf  collections, read-only
//
// WebDAV, SFTP and S3 adapters build on this package.
package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
)

// Kind of a node.
type Kind int

// Node kinds.
const (
	KindRoot Kind = iota
	KindSpace
	KindFolder
	KindFile
	KindCollections // the read-only /@collections directory
	KindCollection  // one collection
)

// CollectionsDir is the top-level directory listing the caller's
// collections. '@' cannot start a space slug, so it never clashes.
const CollectionsDir = "@collections"

// Node is a resolved path.
type Node struct {
	Kind       Kind
	Name       string
	Path       string
	Space      *core.Space
	Folder     *core.FolderInfo // KindSpace (root folder) and KindFolder
	File       *core.FileInfo   // KindFile
	Collection *core.Collection // KindCollection
	ReadOnly   bool             // inside /@collections
	ModTime    time.Time
	Size       int64
}

// IsDir reports whether the node is a directory.
func (n *Node) IsDir() bool { return n.Kind != KindFile }

// FS is the virtual filesystem.
type FS struct {
	Svc *core.Service
}

// New returns a filesystem over svc.
func New(svc *core.Service) *FS { return &FS{Svc: svc} }

// Split splits a vfs path into space slug and segments inside the space.
func Split(p string) (slug string, segs []string, err error) {
	all, err := core.SplitPath(p)
	if err != nil {
		return "", nil, err
	}
	if len(all) == 0 {
		return "", nil, nil
	}
	return all[0], all[1:], nil
}

var startTime = time.Now()

func (fs *FS) space(ctx context.Context, p *core.Principal, slug string) (*core.Space, error) {
	return fs.Svc.GetSpace(ctx, p, slug)
}

// Stat resolves a path.
func (fs *FS) Stat(ctx context.Context, p *core.Principal, path string) (*Node, error) {
	slug, segs, err := Split(path)
	if err != nil {
		return nil, err
	}
	if slug == "" {
		return &Node{Kind: KindRoot, Name: "/", Path: "/", ModTime: startTime}, nil
	}
	if slug == CollectionsDir {
		return fs.statCollections(ctx, p, segs)
	}
	sp, err := fs.space(ctx, p, slug)
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		f, err := fs.Svc.GetFolder(ctx, p, sp.RootFolderID)
		if err != nil {
			return nil, err
		}
		return &Node{Kind: KindSpace, Name: sp.Slug, Path: "/" + sp.Slug, Space: sp, Folder: f, ModTime: sp.CreatedAt}, nil
	}
	folder, ferr := fs.Svc.FolderByPath(ctx, p, sp.ID, segs)
	if ferr == nil {
		return folderNode(sp, folder), nil
	}
	if !errors.Is(ferr, core.ErrNotFound) {
		return nil, ferr
	}
	file, err := fs.Svc.FileByPath(ctx, p, sp.ID, segs)
	if err != nil {
		return nil, err
	}
	return fileNode(sp, file), nil
}

func folderNode(sp *core.Space, f *core.FolderInfo) *Node {
	return &Node{Kind: KindFolder, Name: f.Name, Path: "/" + sp.Slug + strings.TrimSuffix(f.Path, "/"), Space: sp, Folder: f, ModTime: f.CreatedAt}
}

func fileNode(sp *core.Space, f *core.FileInfo) *Node {
	return &Node{Kind: KindFile, Name: f.Name, Path: "/" + sp.Slug + f.Path, Space: sp, File: f, ModTime: f.ModifiedAt, Size: f.Size}
}

// List lists a directory.
func (fs *FS) List(ctx context.Context, p *core.Principal, path string) ([]Node, error) {
	n, err := fs.Stat(ctx, p, path)
	if err != nil {
		return nil, err
	}
	switch n.Kind {
	case KindFile:
		return nil, core.ErrNotFolder
	case KindRoot:
		spaces, err := fs.Svc.ListSpaces(ctx, p)
		if err != nil {
			return nil, err
		}
		out := make([]Node, 0, len(spaces)+1)
		for i := range spaces {
			sp := &spaces[i]
			out = append(out, Node{Kind: KindSpace, Name: sp.Slug, Path: "/" + sp.Slug, Space: sp, ModTime: sp.CreatedAt})
		}
		if cols, _, err := fs.collections(ctx, p); err == nil && len(cols) > 0 {
			out = append(out, *collectionsNode())
		}
		return out, nil
	case KindCollections:
		cols, names, err := fs.collections(ctx, p)
		if err != nil {
			return nil, err
		}
		out := make([]Node, 0, len(cols))
		for _, name := range names {
			c := cols[name]
			out = append(out, *collectionNode(name, &c))
		}
		return out, nil
	case KindCollection:
		return fs.collectionFiles(ctx, p, n)
	}
	_, folders, files, err := fs.Svc.ListFolder(ctx, p, n.Folder.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(folders)+len(files))
	for i := range folders {
		out = append(out, *folderNode(n.Space, &folders[i]))
	}
	for i := range files {
		out = append(out, *fileNode(n.Space, &files[i]))
	}
	return out, nil
}

// Open opens a file for reading.
func (fs *FS) Open(ctx context.Context, p *core.Principal, path string) (*Node, blob.File, error) {
	n, err := fs.Stat(ctx, p, path)
	if err != nil {
		return nil, nil, err
	}
	if n.Kind != KindFile {
		return nil, nil, core.ErrIsFolder
	}
	f, err := fs.Svc.OpenFile(ctx, p, n.File)
	if err != nil {
		return nil, nil, err
	}
	return n, f, nil
}

func (fs *FS) target(ctx context.Context, p *core.Principal, path string) (*core.Space, []string, error) {
	slug, segs, err := Split(path)
	if err != nil {
		return nil, nil, err
	}
	if slug == "" || len(segs) == 0 || slug == CollectionsDir {
		return nil, nil, core.ErrForbidden
	}
	sp, err := fs.space(ctx, p, slug)
	return sp, segs, err
}

// Put writes a file (creating a new version if it exists).
func (fs *FS) Put(ctx context.Context, p *core.Principal, path string, r io.Reader, opt core.PutOptions) (*Node, error) {
	sp, segs, err := fs.target(ctx, p, path)
	if err != nil {
		return nil, err
	}
	res, err := fs.Svc.PutFile(ctx, p, sp.ID, segs, r, opt)
	if err != nil {
		return nil, err
	}
	return fileNode(sp, res.File), nil
}

// PutStaged commits a staged blob at path.
func (fs *FS) PutStaged(ctx context.Context, p *core.Principal, path string, st *blob.Staged, opt core.PutOptions) (*Node, error) {
	sp, segs, err := fs.target(ctx, p, path)
	if err != nil {
		fs.Svc.Blobs.Discard(st)
		return nil, err
	}
	res, err := fs.Svc.PutStaged(ctx, p, sp.ID, segs, st, opt)
	if err != nil {
		return nil, err
	}
	return fileNode(sp, res.File), nil
}

// Mkdir creates a folder; the parent must exist.
func (fs *FS) Mkdir(ctx context.Context, p *core.Principal, path string) error {
	sp, segs, err := fs.target(ctx, p, path)
	if err != nil {
		return err
	}
	parent, err := fs.Svc.FolderByPath(ctx, p, sp.ID, segs[:len(segs)-1])
	if err != nil {
		return err
	}
	_, err = fs.Svc.Mkdir(ctx, p, parent.ID, segs[len(segs)-1])
	return err
}

// MkdirAll creates a folder and all missing parents.
func (fs *FS) MkdirAll(ctx context.Context, p *core.Principal, path string) error {
	sp, segs, err := fs.target(ctx, p, path)
	if err != nil {
		if errors.Is(err, core.ErrForbidden) {
			// "/" or "/<space>" already exist
			_, serr := fs.Stat(ctx, p, path)
			return serr
		}
		return err
	}
	_, err = fs.Svc.MkdirAll(ctx, p, sp.ID, segs)
	return err
}

// Remove deletes a file or (recursively) a folder.
func (fs *FS) Remove(ctx context.Context, p *core.Principal, path string) error {
	n, err := fs.Stat(ctx, p, path)
	if err != nil {
		return err
	}
	if n.ReadOnly {
		return core.ErrForbidden
	}
	switch n.Kind {
	case KindFile:
		return fs.Svc.DeleteLink(ctx, p, n.File.LinkID)
	case KindFolder:
		return fs.Svc.DeleteFolder(ctx, p, n.Folder.ID)
	}
	return core.ErrForbidden
}

// RemoveEmptyDir removes a folder only if it is empty (SFTP rmdir semantics).
func (fs *FS) RemoveEmptyDir(ctx context.Context, p *core.Principal, path string) error {
	n, err := fs.Stat(ctx, p, path)
	if err != nil {
		return err
	}
	if n.Kind != KindFolder {
		if n.Kind == KindFile {
			return core.ErrNotFolder
		}
		return core.ErrForbidden
	}
	_, folders, files, err := fs.Svc.ListFolder(ctx, p, n.Folder.ID)
	if err != nil {
		return err
	}
	if len(folders)+len(files) > 0 {
		return core.ErrNotEmpty
	}
	return fs.Svc.DeleteFolder(ctx, p, n.Folder.ID)
}

// destination resolves the parent folder and name of a move/copy target.
func (fs *FS) destination(ctx context.Context, p *core.Principal, to string) (*core.Space, *core.FolderInfo, string, *Node, error) {
	sp, segs, err := fs.target(ctx, p, to)
	if err != nil {
		return nil, nil, "", nil, err
	}
	parent, err := fs.Svc.FolderByPath(ctx, p, sp.ID, segs[:len(segs)-1])
	if err != nil {
		return nil, nil, "", nil, err
	}
	existing, err := fs.Stat(ctx, p, to)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, nil, "", nil, err
	}
	return sp, parent, segs[len(segs)-1], existing, nil
}

// Rename moves a file or folder. overwrite controls replacing an existing
// destination: files receive the source content as a new version, folders
// are replaced.
func (fs *FS) Rename(ctx context.Context, p *core.Principal, from, to string, overwrite bool) error {
	src, err := fs.Stat(ctx, p, from)
	if err != nil {
		return err
	}
	if src.Kind == KindRoot || src.Kind == KindSpace || src.ReadOnly || src.Kind == KindCollections {
		return core.ErrForbidden
	}
	_, parent, name, existing, err := fs.destination(ctx, p, to)
	if err != nil {
		return err
	}
	if existing != nil && existing.Path == src.Path {
		return nil
	}
	if src.Kind == KindFile {
		if existing != nil && existing.IsDir() {
			if !overwrite {
				return core.ErrConflict
			}
			if err := fs.Remove(ctx, p, to); err != nil {
				return err
			}
		}
		_, err = fs.Svc.MoveLink(ctx, p, src.File.LinkID, parent.ID, name, overwrite)
		return err
	}
	if existing != nil {
		if !overwrite {
			return core.ErrConflict
		}
		if err := fs.Remove(ctx, p, to); err != nil {
			return err
		}
	}
	_, err = fs.Svc.MoveFolder(ctx, p, src.Folder.ID, parent.ID, name)
	return err
}

// Copy copies a file or folder.
func (fs *FS) Copy(ctx context.Context, p *core.Principal, from, to string, overwrite bool) error {
	src, err := fs.Stat(ctx, p, from)
	if err != nil {
		return err
	}
	if src.Kind == KindRoot || src.Kind == KindSpace || src.Kind == KindCollections || src.Kind == KindCollection {
		return core.ErrForbidden
	}
	_, parent, name, existing, err := fs.destination(ctx, p, to)
	if err != nil {
		return err
	}
	if src.Kind == KindFile {
		if existing != nil && existing.IsDir() {
			if !overwrite {
				return core.ErrConflict
			}
			if err := fs.Remove(ctx, p, to); err != nil {
				return err
			}
		}
		_, err = fs.Svc.CopyLink(ctx, p, src.File.LinkID, parent.ID, name, overwrite)
		return err
	}
	if existing != nil {
		if !overwrite {
			return core.ErrConflict
		}
		if err := fs.Remove(ctx, p, to); err != nil {
			return err
		}
	}
	_, err = fs.Svc.CopyFolder(ctx, p, src.Folder.ID, parent.ID, name)
	return err
}

// ---------------------------------------------------------------- collections

func collectionsNode() *Node {
	return &Node{Kind: KindCollections, Name: CollectionsDir, Path: "/" + CollectionsDir, ModTime: startTime, ReadOnly: true}
}

func collectionNode(name string, c *core.Collection) *Node {
	return &Node{Kind: KindCollection, Name: name, Path: "/" + CollectionsDir + "/" + name, Collection: c,
		ModTime: c.CreatedAt, ReadOnly: true}
}

// collections returns the caller's collections by directory name. A team
// collection whose name is also used by another collection is shown as
// "name (team)".
func (fs *FS) collections(ctx context.Context, p *core.Principal) (map[string]core.Collection, []string, error) {
	cols, err := fs.Svc.ListCollections(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	used := map[string]int{}
	for _, c := range cols {
		used[c.Name]++
	}
	out := map[string]core.Collection{}
	var names []string
	for _, c := range cols {
		name := c.Name
		if used[name] > 1 && c.OwnerTeamID != nil {
			name = fmt.Sprintf("%s (%s)", c.Name, c.TeamName)
		}
		if _, dup := out[name]; dup {
			continue
		}
		out[name] = c
		names = append(names, name)
	}
	sort.Strings(names)
	return out, names, nil
}

func (fs *FS) statCollections(ctx context.Context, p *core.Principal, segs []string) (*Node, error) {
	if len(segs) == 0 {
		return collectionsNode(), nil
	}
	cols, _, err := fs.collections(ctx, p)
	if err != nil {
		return nil, err
	}
	c, ok := cols[segs[0]]
	if !ok {
		return nil, core.ErrNotFound
	}
	dir := collectionNode(segs[0], &c)
	switch len(segs) {
	case 1:
		return dir, nil
	case 2:
		files, err := fs.collectionFiles(ctx, p, dir)
		if err != nil {
			return nil, err
		}
		for i := range files {
			if files[i].Name == segs[1] {
				return &files[i], nil
			}
		}
	}
	return nil, core.ErrNotFound
}

// collectionFiles lists the documents of a collection under unique names
// ("name (2).ext" for clashes).
func (fs *FS) collectionFiles(ctx context.Context, p *core.Principal, dir *Node) ([]Node, error) {
	items, err := fs.Svc.CollectionItems(ctx, p, dir.Collection.ID)
	if err != nil {
		return nil, err
	}
	spaces, err := fs.Svc.ListSpaces(ctx, p)
	if err != nil {
		return nil, err
	}
	bySpace := map[string]*core.Space{}
	for i := range spaces {
		bySpace[spaces[i].ID] = &spaces[i]
	}
	used := map[string]bool{}
	out := make([]Node, 0, len(items))
	for i := range items {
		f := items[i].FileInfo
		name := f.Name
		ext := path.Ext(name)
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(f.Name, ext), n, ext)
		}
		used[name] = true
		out = append(out, Node{Kind: KindFile, Name: name, Path: dir.Path + "/" + name, Space: bySpace[f.SpaceID],
			File: &f, ModTime: f.ModifiedAt, Size: f.Size, ReadOnly: true})
	}
	return out, nil
}
