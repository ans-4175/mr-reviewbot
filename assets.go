// Package reviewbot embeds the prompts and skills so the binary is self-contained.
package reviewbot

import "embed"

//go:embed prompts/system.md skills/review-mr skills/thirdparty/addyosmani-agent-skills
var Assets embed.FS
