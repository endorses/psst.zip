package source

import (
	"fmt"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

// OriginalReader shares the already authenticated member indices and selected
// source bytes among higher verifiers. Inspecting a member fact does not expand
// its original again. Read should receive a batch of all needed text members.
type OriginalReader struct {
	files         map[string][]byte
	items         map[string]preferredOriginal
	links         map[string]map[string]string
	cache         map[string]map[string][]byte
	relationships map[string]Relationship
}

type Relationship struct{ Kind, Name, Version, Integrity, ConfigurationSHA256 string }

func NewOriginalReader(inventory Inventory, files map[string][]byte) (*OriginalReader, error) {
	items, err := namedSources(inventory, files, "upstream")
	if err != nil {
		return nil, err
	}
	reader := &OriginalReader{files: files, items: items, links: map[string]map[string]string{}, cache: map[string]map[string][]byte{}, relationships: map[string]Relationship{}}
	var catalog upstreamCatalog
	if err := contract.Decode(files["inputs/"+catalogPath], MetadataLimit, &catalog); err != nil {
		return nil, err
	}
	for _, record := range catalog.Upstreams {
		reader.links[record.ID] = record.FixtureLinks
		if value := record.Relationship; value != nil {
			reader.relationships[record.ID] = Relationship{value.Kind, value.Name, value.Version, value.Integrity, value.ConfigurationSHA256}
		}
	}
	return reader, nil
}

func (r *OriginalReader) Relationship(id string) (Relationship, error) {
	item, found := r.relationships[id]
	if !found {
		return item, fmt.Errorf("missing reviewed upstream component relationship")
	}
	return item, nil
}

func (r *OriginalReader) Item(id string) (Source, error) {
	item, found := r.items[id]
	if !found {
		return Source{}, fmt.Errorf("missing retained upstream source %s", id)
	}
	return item.source, nil
}

func (r *OriginalReader) Fact(id, name string) (File, error) {
	item, found := r.items[id]
	if !found {
		return File{}, fmt.Errorf("missing retained upstream source %s", id)
	}
	files, err := preferredFacts(item.members, []string{name})
	if err != nil {
		return File{}, err
	}
	return files[0], nil
}

func (r *OriginalReader) Read(id string, names []string) (map[string][]byte, error) {
	item, found := r.items[id]
	if !found {
		return nil, fmt.Errorf("missing retained upstream source %s", id)
	}
	selected := map[string]bool{}
	for _, name := range names {
		if _, err := r.Fact(id, name); err != nil {
			return nil, err
		}
		if _, found := r.cache[id][name]; !found {
			selected[name] = true
		}
	}
	if len(selected) > 0 {
		raw := r.files[item.source.Original.Path]
		if fact(item.source.Original.Path, raw) != item.source.Original {
			return nil, fmt.Errorf("retained upstream original changed")
		}
		_, captured, err := inspectArchive(raw, archivePolicy{Expanded: 512 << 20, Member: OriginalLimit, Members: 100000, Prefix: item.root, Commit: item.source.Version, Original: true, Links: r.links[id], Selected: selected})
		if err != nil {
			return nil, err
		}
		if len(captured) != len(selected) {
			return nil, fmt.Errorf("missing selected upstream text inputs")
		}
		if r.cache[id] == nil {
			r.cache[id] = map[string][]byte{}
		}
		for name, body := range captured {
			want, err := r.Fact(id, name)
			if err != nil || fact(name, body) != want {
				return nil, fmt.Errorf("retained upstream member differs")
			}
			r.cache[id][name] = body
		}
	}
	result := map[string][]byte{}
	for _, name := range names {
		result[name] = r.cache[id][name]
	}
	return result, nil
}
