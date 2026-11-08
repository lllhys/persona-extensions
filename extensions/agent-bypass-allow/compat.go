package main

import "strings"

// Legacy hook ownership is deliberately independent of the public extension ID.
// Version 0.2.23 recognizes only this suffix: keeping it on generated commands
// lets an old package replace or remove our hook after a rollback. Do not turn
// an ID rename into a second hook or host-side edits to extension-private files.
const legacyExtensionID = "claude-bypass-allow"
const currentExtensionID = "agent-bypass-allow"
const tagFlag = "--persona-extension=" + legacyExtensionID
const renamedTagFlag = "--persona-extension=" + currentExtensionID
const settingsBackupSuffix = "." + legacyExtensionID + ".bak"
const settingsTempPattern = ".settings-" + legacyExtensionID + "-*"

func isOurCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	return strings.HasSuffix(cmd, " "+tagFlag) || strings.HasSuffix(cmd, " "+renamedTagFlag)
}
