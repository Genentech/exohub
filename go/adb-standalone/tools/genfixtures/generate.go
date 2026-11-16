package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// document is a loosely-typed synthetic ADB document, matching the shape
// consumed by internal/seed.Seed and produced by internal/ingest.
type document map[string]any

// genState holds the deterministic base time used while building the
// synthetic corpus. All per-document variation is derived from sha256 hashes
// of stable labels (see synthHash/timeAt/hashByte) rather than a PRNG stream,
// so output never depends on call order — only on genSeed and the corpus
// definition in corpus.go.
type genState struct {
	baseTime  time.Time
	indexName string
}

func newGenState() *genState {
	return &genState{
		baseTime:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		indexName: "adb-standalone-genfixtures-v1",
	}
}

// synthHash deterministically derives a 40-char hex string (shaped like a git
// SHA-1) from a label. Never derived from or equal to any real hash.
func synthHash(label string) string {
	sum := sha256.Sum256([]byte("genfixtures-hash:" + label))
	return hex.EncodeToString(sum[:])[:40]
}

// synthUUID deterministically derives a UUID-shaped string from a label.
func synthUUID(label string) string {
	sum := sha256.Sum256([]byte("genfixtures-uuid:" + label))
	h := hex.EncodeToString(sum[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// hashByte derives a small deterministic byte from a label, used to vary
// sizes/subject picks without a shared mutable counter.
func hashByte(label string) int {
	sum := sha256.Sum256([]byte("genfixtures-byte:" + label))
	return int(sum[0])
}

// buildCorpus generates the full synthetic document set, in the order it is
// written to seed/documents.ndjson: grouped by project, then by version.
func buildCorpus() []document {
	gs := newGenState()
	var docs []document

	for pIdx, proj := range corpusProjects {
		versions := synthVersions(pIdx)
		for vIdx, version := range versions {
			isLatest := vIdx == len(versions)-1
			fromRef := ""
			if vIdx > 0 {
				fromRef = versions[vIdx-1]
			}
			docs = append(docs, gs.bundleDoc(proj, version, fromRef, isLatest, vIdx))
			docs = append(docs, gs.commitDocs(proj, version, isLatest)...)
			docs = append(docs, gs.artifactDocs(proj, version, isLatest)...)
		}
	}
	return docs
}

func adbID(projectID, idPath, version string) string {
	return fmt.Sprintf("%s:%s@%s", projectID, idPath, version)
}

func gprn(projectID, idPath, version string) string {
	return fmt.Sprintf("gprn:wip:adb::artifact:%s", adbID(projectID, idPath, version))
}

// timeAt deterministically derives a timestamp from inputs, offset by
// offsetSeconds, so every doc gets a distinct but reproducible time.
func (gs *genState) timeAt(label string, offsetSeconds int) time.Time {
	sum := sha256.Sum256([]byte("genfixtures-time:" + label))
	days := int(sum[0]) % 300
	secs := int(sum[1])*256 + int(sum[2])
	return gs.baseTime.AddDate(0, 0, days).
		Add(time.Duration(secs) * time.Second).
		Add(time.Duration(offsetSeconds) * time.Second)
}

func (gs *genState) extraBase(proj project, metapath, idPath, version, schema, docType string, latest bool) map[string]any {
	label := idPath + "@" + version
	return map[string]any{
		"$schema":    schema,
		"type":       docType,
		"id":         adbID(proj.id, idPath, version),
		"gprn":       gprn(proj.id, idPath, version),
		"project_id": proj.id,
		"version":    version,
		"latest":     latest,
		"permissions": map[string]any{
			"read_access":  "public",
			"write_access": "owners",
			"scope":        "project",
			"owners":       append([]string{}, synthOwners...),
		},
		"location": map[string]any{
			"type": "s3",
			"s3":   map[string]any{"bucket": synthBucket},
		},
		"tenant":        map[string]any{"path": "/exohub", "alias": "exohub"},
		"metapath":      metapath,
		"index_name":    gs.indexName,
		"meta_indexed":  gs.timeAt(label, 0).Format(time.RFC3339Nano),
		"meta_uploaded": gs.timeAt(label, 0).Format(time.RFC3339),
		"file_size":     nil,
	}
}

func (gs *genState) bundleDoc(proj project, version, fromRef string, latest bool, vIdx int) document {
	const schema = "exohub-bundle/v1.json"
	metapath := "bundle.json"
	extra := gs.extraBase(proj, metapath, metapath, version, schema, "exohub bundle", latest)

	commitHashes := make([]string, 0, len(proj.paths)+1)
	bundleCommit := synthHash(proj.id + "/" + version + "/bundle-commit")
	commitHashes = append(commitHashes, bundleCommit)
	for _, p := range proj.paths {
		commitHashes = append(commitHashes, synthHash(proj.id+"/"+version+"/"+p))
	}

	doc := document{
		"ref_name":            version,
		"file_count":          len(proj.paths),
		"repo_url":            fmt.Sprintf("git@%s:%s/%s.git", proj.repoHost, proj.repoOrg, proj.id),
		"commit_hashes":       commitHashes,
		"total_size":          100000 + vIdx*12345,
		"markdown_file_count": 0,
		"preset":              "default",
		"incremental":         vIdx > 0,
		"annex_file_count":    len(proj.paths),
		"exo_version":         "0.1.0",
		"commit_count":        len(commitHashes),
		"path":                metapath,
		"ref":                 bundleCommit,
		"generated_at":        gs.timeAt(metapath+"@"+version, 0).Format(time.RFC3339),
		"scope":               "annex",
		"git_file_count":      0,
		"ref_type":            "tag",
		"_extra":              extra,
	}
	if fromRef != "" {
		doc["from_ref"] = fromRef
	}
	return doc
}

func (gs *genState) commitDocs(proj project, version string, latest bool) []document {
	const schema = "exohub-commit/v1.json"
	const n = 2
	docs := make([]document, 0, n)
	for i := 0; i < n; i++ {
		hash := synthHash(fmt.Sprintf("%s/%s/commit/%d", proj.id, version, i))
		parentHash := synthHash(fmt.Sprintf("%s/%s/commit/%d", proj.id, version, i+1))
		relPath := fmt.Sprintf("commits/%s.json", hash)
		extra := gs.extraBase(proj, relPath, relPath, version, schema, "exohub commit", latest)

		at := gs.timeAt(relPath+"@"+version, i).Format(time.RFC3339)
		subject := synthCommitSubjects[hashByte(relPath)%len(synthCommitSubjects)]

		docs = append(docs, document{
			"author_date":     at,
			"author_email":    synthAuthorEmail,
			"author_name":     synthAuthorName,
			"body":            "",
			"committer_date":  at,
			"committer_email": synthAuthorEmail,
			"committer_name":  synthAuthorName,
			"hash":            hash,
			"parents":         []string{parentHash},
			"path":            relPath,
			"ref":             hash,
			"repo_url":        fmt.Sprintf("git@%s:%s/%s.git", proj.repoHost, proj.repoOrg, proj.id),
			"subject":         subject,
			"_extra":          extra,
		})
	}
	return docs
}

func (gs *genState) artifactDocs(proj project, version string, latest bool) []document {
	const schema = "exohub-artifact/v1.json"
	docs := make([]document, 0, len(proj.paths))
	for _, p := range proj.paths {
		metapath := "files/" + p + ".json"
		extra := gs.extraBase(proj, metapath, p, version, schema, "exohub artifact", latest)

		label := proj.id + "/" + version + "/" + p
		size := 1000 + hashByte(label)*137
		chunkKey := fmt.Sprintf("SHA256E-s%d-S1073741824-C1--%s", size, synthHash(label+"/content"))
		key := fmt.Sprintf("SHA256E-s%d--%s", size, synthHash(label+"/key"))
		s3url := fmt.Sprintf("s3://%s/annex/%s", synthBucket, proj.id)

		doc := document{
			"backend": "SHA256E",
			"key":     key,
			"locations": []any{
				map[string]any{
					"type":   "annex",
					"remote": synthRemoteType,
					"s3url":  s3url,
					"uuid":   synthUUID(label),
					"chunks": []any{
						map[string]any{
							"chunk_key":    chunkKey,
							"chunk_number": 1,
							"chunk_size":   size,
							"s3_path":      s3url + "/" + chunkKey,
						},
					},
				},
			},
			"path":     p,
			"ref":      synthHash(proj.id + "/" + version + "/bundle-commit"),
			"repo_url": fmt.Sprintf("git@%s:%s/%s.git", proj.repoHost, proj.repoOrg, proj.id),
			"size":     size,
			"_extra":   extra,
		}
		docs = append(docs, doc)
	}
	return docs
}
