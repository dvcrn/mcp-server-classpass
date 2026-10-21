package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// marketingTagID is a category of several hundred promotional labels that are not useful for searching.
const marketingTagID = "17142"

type listTagsInput struct {
	Category string `json:"category,omitempty" jsonschema:"category name or tag id to expand, e.g. Food and Drink or 1143; omit for the overview"`
}

// tagEntry is one tag in a flat listing; Path names its ancestors, e.g. "Experiences > Massage".
type tagEntry struct {
	TagID string `json:"tag_id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
}

type listTagsOutput struct {
	Tags []tagEntry `json:"tags"`
}

func (h *handlers) listTags(ctx context.Context, _ *mcp.CallToolRequest, in listTagsInput) (*mcp.CallToolResult, listTagsOutput, error) {
	tags, err := h.cp.Tags(ctx)
	if err != nil {
		return nil, listTagsOutput{}, err
	}

	if in.Category != "" {
		t := lookupTag(tags, in.Category)
		if t == nil {
			return nil, listTagsOutput{}, fmt.Errorf("no tag matches %q; call list_tags without category for the overview", in.Category)
		}

		return nil, listTagsOutput{Tags: flattenTags(nil, *t, "", -1)}, nil
	}

	// The overview lists categories with their direct subtags; standalone flags and marketing labels are left out.
	out := listTagsOutput{Tags: []tagEntry{}}
	for _, t := range tags {
		if len(t.Subtags) == 0 || t.ID == marketingTagID {
			continue
		}

		out.Tags = flattenTags(out.Tags, t, "", 1)
	}

	return nil, out, nil
}

// flattenTags appends t and up to depth levels of its subtags in tree order; a negative depth keeps them all.
func flattenTags(dst []tagEntry, t classpass.Tag, parent string, depth int) []tagEntry {
	path := t.Name
	if parent != "" {
		path = parent + " > " + t.Name
	}

	dst = append(dst, tagEntry{TagID: t.ID, Name: t.Name, Path: path})
	if depth == 0 {
		return dst
	}

	for _, s := range t.Subtags {
		dst = flattenTags(dst, s, path, depth-1)
	}

	return dst
}

// lookupTag finds a tag anywhere in the tree by id or case-insensitive name.
func lookupTag(tags []classpass.Tag, key string) *classpass.Tag {
	for i := range tags {
		if tags[i].ID == key || strings.EqualFold(tags[i].Name, key) {
			return &tags[i]
		}

		if t := lookupTag(tags[i].Subtags, key); t != nil {
			return t
		}
	}

	return nil
}

// findTag resolves a tag id from list_tags.
func findTag(ctx context.Context, cp *classpass.Client, id string) (*classpass.Tag, error) {
	tags, err := cp.Tags(ctx)
	if err != nil {
		return nil, err
	}

	t := lookupTag(tags, id)
	if t == nil || t.ID != id {
		return nil, fmt.Errorf("unknown tag_id %q; see list_tags", id)
	}

	return t, nil
}

// suggestTags returns up to 5 tags sharing a word stem with query, e.g. "drinks" suggests Food and Drink.
func suggestTags(ctx context.Context, cp *classpass.Client, query string) []string {
	tags, err := cp.Tags(ctx)
	if err != nil {
		return nil
	}

	var related []string
	var walk func([]classpass.Tag)
	walk = func(ts []classpass.Tag) {
		for _, t := range ts {
			if len(related) == 5 {
				return
			}

			if t.ID == marketingTagID {
				continue
			}

			if sharesStem(query, t.Name) {
				related = append(related, fmt.Sprintf("%s (tag_id %s)", t.Name, t.ID))
			}

			walk(t.Subtags)
		}
	}

	walk(tags)

	return related
}

// sharesStem reports whether a word of a is a prefix of a word of b or the reverse. Words under 4 letters are
// ignored so "and" or "spa" do not match everything.
func sharesStem(a, b string) bool {
	for _, x := range strings.Fields(strings.ToLower(a)) {
		for _, y := range strings.Fields(strings.ToLower(b)) {
			if len(x) >= 4 && len(y) >= 4 && (strings.HasPrefix(x, y) || strings.HasPrefix(y, x)) {
				return true
			}
		}
	}

	return false
}
