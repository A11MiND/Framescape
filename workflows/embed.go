// Package workflows embeds the aether/v1 workflow documents in this
// directory into the binary, so it is the single source of truth (PRD §15:
// "workflows/*.json 要进 git 并做 CI 静态校验，工作流定义就是契约") — no
// package copies these files, they all read from here.
package workflows

import "embed"

//go:embed *.json
var FS embed.FS
