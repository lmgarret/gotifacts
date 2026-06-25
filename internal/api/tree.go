package api

import (
	"sort"
	"strings"

	"github.com/lmgarret/gotifacts/internal/store"
)

// TreeNode is a node in the nested group tree returned by GET /api/sites.
type TreeNode struct {
	// Name is the group segment label for this node ("" for the root).
	Name string `json:"name"`
	// Path is the full group path to this node (e.g. "grp/sub").
	Path string `json:"path"`
	// Site is the group's own landing site, if a flat/leaf site shares this
	// node's path (e.g. the site "decks" for the group "decks"). nil otherwise.
	Site *store.Site `json:"site,omitempty"`
	// Groups are child group nodes, sorted by name.
	Groups []*TreeNode `json:"groups"`
	// Sites are the leaf sites directly within this group, sorted by slug.
	Sites []store.Site `json:"sites"`
}

// BuildTree assembles a nested group tree from a flat list of sites.
func BuildTree(sites []store.Site) *TreeNode {
	root := &TreeNode{Name: "", Path: "", Groups: []*TreeNode{}, Sites: []store.Site{}}
	index := map[string]*TreeNode{"": root}

	ensure := func(path string) *TreeNode {
		if n, ok := index[path]; ok {
			return n
		}
		// Build ancestors as needed.
		segs := strings.Split(path, "/")
		cur := root
		acc := ""
		for _, seg := range segs {
			if acc == "" {
				acc = seg
			} else {
				acc += "/" + seg
			}
			child, ok := index[acc]
			if !ok {
				child = &TreeNode{Name: seg, Path: acc, Groups: []*TreeNode{}, Sites: []store.Site{}}
				index[acc] = child
				cur.Groups = append(cur.Groups, child)
			}
			cur = child
		}
		return cur
	}

	for _, site := range sites {
		node := root
		if site.Group != "" {
			node = ensure(site.Group)
		}
		node.Sites = append(node.Sites, site)
	}

	// Merge a flat/leaf site into the same-named group node: if a site's full
	// path (group/slug) equals an existing group node's path, it is that group's
	// landing site rather than a sibling card. e.g. site "decks" + group "decks".
	for path, node := range index {
		if path == "" {
			continue
		}
		parentPath, slug := splitGroupPath(path)
		parent, ok := index[parentPath]
		if !ok {
			continue
		}
		for i := range parent.Sites {
			if parent.Sites[i].Group == parentPath && parent.Sites[i].Slug == slug {
				s := parent.Sites[i]
				node.Site = &s
				parent.Sites = append(parent.Sites[:i], parent.Sites[i+1:]...)
				break
			}
		}
	}

	sortNode(root)
	return root
}

// splitGroupPath splits a group path into its parent path and final segment,
// e.g. "grp/sub" → ("grp", "sub"), "decks" → ("", "decks").
func splitGroupPath(path string) (parent, last string) {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[:i], path[i+1:]
	}
	return "", path
}

func sortNode(n *TreeNode) {
	sort.Slice(n.Groups, func(i, j int) bool { return n.Groups[i].Name < n.Groups[j].Name })
	sort.Slice(n.Sites, func(i, j int) bool { return n.Sites[i].Slug < n.Sites[j].Slug })
	for _, g := range n.Groups {
		sortNode(g)
	}
}
