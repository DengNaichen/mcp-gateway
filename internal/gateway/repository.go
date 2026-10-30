package gateway

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var branchName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
var secretEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var gitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

func validateRepository(r RepositorySource) error {
	if r.URL == "" || len(r.Files) == 0 || len(r.Files) > 32 {
		return invalid("仓库来源需要 URL 或绝对本地路径，以及至少一个文件（最多 32 个）")
	}
	if !filepath.IsAbs(r.URL) {
		u, err := url.Parse(r.URL)
		if err != nil || !strings.HasPrefix(r.URL, "ssh://") && (!strings.HasPrefix(r.URL, "https://") && !strings.HasPrefix(r.URL, "http://")) {
			return invalid("仓库地址必须是绝对本地路径或 HTTP(S)/SSH URL")
		}
		if u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return invalid("仓库地址无效")
		}
		if u.User != nil {
			if _, set := u.User.Password(); set || u.Scheme != "ssh" {
				return invalid("Git 凭据请通过本机 credential helper / SSH 配置提供")
			}
		}
	}
	if !branchName.MatchString(r.Ref) || strings.Contains(r.Ref, "..") || strings.Contains(r.Ref, "//") || strings.HasSuffix(r.Ref, ".") || strings.HasSuffix(r.Ref, "/") || strings.Contains(r.Ref, ".lock") {
		return invalid("仓库 ref 必须是有效分支名或 commit SHA")
	}
	for _, file := range r.Files {
		if file == "" || filepath.IsAbs(file) || filepath.ToSlash(filepath.Clean(file)) != file || file == ".." || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\x00\n\r") {
			return invalid("仓库文件必须是不含 .. 的相对路径")
		}
	}
	if r.EventsEnabled && (r.Project == "" || !secretEnvName.MatchString(r.WebhookSecretEnv)) {
		return invalid("开启仓库事件需要 project 和 webhook 密钥环境变量名称")
	}
	return nil
}
func (m *Maintainer) repositoryDocuments(ctx context.Context, r *MaintenanceRun) ([]sourceDocument, error) {
	source := r.Source.Repository
	if source == nil {
		return nil, nil
	}
	dir, err := filepath.Abs(filepath.Join(m.g.dir, "maintenance", "repositories", maintenanceDigest(source.URL)[:24]))
	if err != nil {
		return nil, fmt.Errorf("resolve repository cache: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create repository cache: %w", err)
	}
	base := []string{"-c", "core.hooksPath=/dev/null", "-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=always"}
	git := func(args ...string) ([]byte, error) {
		return runCommand(ctx, "git", append(append([]string{}, base...), args...), dir, nil, map[string]string{"GIT_TERMINAL_PROMPT": "0"}, 2<<20)
	}
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); os.IsNotExist(err) {
		if _, err := git("init", "--bare", "--template=", dir); err != nil {
			return nil, fmt.Errorf("initialize repository cache: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("inspect repository cache: %w", err)
	}
	if _, err := git("fetch", "--no-tags", "--no-recurse-submodules", "--depth=1", "--", source.URL, source.Ref); err != nil {
		return nil, fmt.Errorf("fetch configured repository ref (check Git credentials and branch): %w", err)
	}
	sha, err := git("rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve fetched commit: %w", err)
	}
	commit := strings.TrimSpace(string(sha))
	if !gitSHA.MatchString(commit) {
		return nil, invalid("Git 返回了无效 commit ID")
	}
	r.Provenance.Repository = source.URL
	r.Provenance.Ref = source.Ref
	r.Provenance.Commit = commit
	docs := []sourceDocument{}
	for _, name := range source.Files {
		content, err := git("show", commit+":"+name)
		if err != nil {
			return nil, fmt.Errorf("read repository file %s: %w", name, err)
		}
		docs = append(docs, sourceDocument{Name: "repository:" + name, Content: string(content)})
	}
	return docs, nil
}
