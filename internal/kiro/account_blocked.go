package kiro

import "time"

const (
	// blockedNote is shown as the credential note in the management panel,
	// the only free-text field CPA reads back from a plugin auth file.
	blockedNote  = "Kiro 拒绝了该账号（HTTP 403 User is not authorized），可能已被封禁；确认解封后请手动启用"
	blockedAtKey = "kiro4cpa_blocked_at"
)

// markAccountBlocked disables the credential and records why. A note the user
// wrote is kept; the reason then only appears in the plugin log.
func markAccountBlocked(fields, metadata map[string]any, now time.Time) (map[string]any, map[string]any) {
	fields = cloneMap(fields)
	fields["disabled"] = true
	fields[blockedAtKey] = now.UTC().Format(time.RFC3339)
	metadata["disabled"] = true
	if note, _ := fields["note"].(string); note == "" || note == blockedNote {
		fields["note"] = blockedNote
		metadata["note"] = blockedNote
	}
	return fields, metadata
}

func blockedNoteSet(fields map[string]any) bool {
	_, ok := fields[blockedAtKey]
	return ok
}
