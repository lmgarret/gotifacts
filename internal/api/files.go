package api

import (
	"archive/zip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lmgarret/gotifacts/internal/auth"
	"github.com/lmgarret/gotifacts/internal/ingest"
	"github.com/lmgarret/gotifacts/internal/router"
	"github.com/lmgarret/gotifacts/internal/store"
)

// FileNode is a node in a revision's file tree. Directories carry Children;
// regular files carry a Size. Path is the slash-separated path relative to the
// revision root ("" for the root node).
type FileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Dir      bool        `json:"dir"`
	Size     int64       `json:"size,omitempty"`
	Children []*FileNode `json:"children,omitempty"`
}

// handleSiteGet is the viewer-plane dispatcher for GET /api/sites/{rest...}.
// It serves the revision-browsing endpoints, keyed off a "revisions" segment:
//
//	{group/slug}/revisions                     -> list revisions
//	{group/slug}/revisions/{rev}/files         -> file tree
//	{group/slug}/revisions/{rev}/file?path=... -> download one file
//	{group/slug}/revisions/{rev}/archive       -> download revision as zip
func (s *Server) handleSiteGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	rest := strings.Trim(r.PathValue("rest"), "/")
	segs := strings.Split(rest, "/")

	idx := -1
	for i, seg := range segs {
		if seg == "revisions" {
			idx = i
			break
		}
	}
	// Need at least one segment (the slug) before "revisions".
	if idx < 1 {
		http.NotFound(w, r)
		return
	}
	siteSegs := segs[:idx]
	sub := segs[idx+1:]

	slug := siteSegs[len(siteSegs)-1]
	group := strings.Join(siteSegs[:len(siteSegs)-1], "/")
	sp, err := router.NewSitePath(group, slug)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid site path")
		return
	}

	// Resolve the site and apply the same visibility rule as the listing:
	// hidden sites are invisible to non-admins.
	site, err := s.store.GetSite(r.Context(), sp.Group, sp.Slug)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read site")
		return
	}
	if site.Hidden && !p.Admin {
		http.NotFound(w, r)
		return
	}

	switch {
	case len(sub) == 0:
		s.listRevisions(w, r, sp)
	case len(sub) == 2 && sub[1] == "files":
		s.revisionFiles(w, r, sp, sub[0])
	case len(sub) == 2 && sub[1] == "file":
		s.revisionFile(w, r, sp, sub[0])
	case len(sub) == 2 && sub[1] == "archive":
		s.revisionArchive(w, r, sp, sub[0], site.Slug)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) listRevisions(w http.ResponseWriter, _ *http.Request, sp router.SitePath) {
	revs, err := s.pub.ListRevisions(sp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list revisions")
		return
	}
	if revs == nil {
		revs = []ingest.Revision{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revs})
}

func (s *Server) revisionFiles(w http.ResponseWriter, r *http.Request, sp router.SitePath, rev string) {
	root, err := s.pub.RevisionDir(sp, rev)
	if errors.Is(err, ingest.ErrRevisionNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve revision")
		return
	}
	tree, err := buildFileTree(root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read revision files")
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

func (s *Server) revisionFile(w http.ResponseWriter, r *http.Request, sp router.SitePath, rev string) {
	root, err := s.pub.RevisionDir(sp, rev)
	if errors.Is(err, ingest.ErrRevisionNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve revision")
		return
	}
	target, err := safeJoin(root, r.URL.Query().Get("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(target)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(target)+"\"")
	http.ServeFile(w, r, target)
}

func (s *Server) revisionArchive(w http.ResponseWriter, r *http.Request, sp router.SitePath, rev, slug string) {
	root, err := s.pub.RevisionDir(sp, rev)
	if errors.Is(err, ingest.ErrRevisionNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve revision")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+slug+"-"+rev+".zip\"")

	zw := zip.NewWriter(w)
	defer func() { _ = zw.Close() }()
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fw, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		_, err = io.Copy(fw, in)
		return err
	})
}

// buildFileTree walks root and returns its file tree as a single root FileNode.
func buildFileTree(root string) (*FileNode, error) {
	rootNode := &FileNode{Name: "", Path: "", Dir: true}
	nodes := map[string]*FileNode{"": rootNode}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		parentPath := filepath.ToSlash(filepath.Dir(rel))
		if parentPath == "." {
			parentPath = ""
		}
		parent := nodes[parentPath]
		node := &FileNode{Name: d.Name(), Path: rel, Dir: d.IsDir()}
		if d.IsDir() {
			nodes[rel] = node
		} else {
			if info, ierr := d.Info(); ierr == nil {
				node.Size = info.Size()
			}
		}
		parent.Children = append(parent.Children, node)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortFileNode(rootNode)
	return rootNode, nil
}

// sortFileNode orders each directory's children with sub-directories first,
// then files, alphabetically within each group.
func sortFileNode(n *FileNode) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		if c.Dir {
			sortFileNode(c)
		}
	}
}

// safeJoin cleans rel and joins it under root, rejecting any result that would
// escape root (path traversal).
func safeJoin(root, rel string) (string, error) {
	clean := filepath.Clean("/" + rel)
	target := filepath.Join(root, filepath.FromSlash(clean))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", errors.New("path escapes revision root")
	}
	return target, nil
}
