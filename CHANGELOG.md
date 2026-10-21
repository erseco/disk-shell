# Changelog

v1.3.0

### Added

- Live header graphs with automatically scaled one-minute histories for every logical CPU core, RAM, volume usage, fan speed, and available drive temperatures.
- Authenticated metrics streaming over WebSocket with a shared sampler and keyboard-accessible detail panels for individual readings.
- Responsive monitor widgets that hide from right to left as the window narrows.
- Separate sudo shells opened with a plus-and-shield button and a localized password dialog. Sudo runs as the authenticated DSM account and applies its normal policy; the password is passed through a one-shot pipe separately from terminal input.
- German and English labels for the new controls and dialogs.

### Fixed

- Closing a nonpersistent shell now terminates foreground and background jobs, including nested child sessions. Unresponsive jobs are killed after a grace period. Keeping a shell running requires explicit background persistence.
- Stalled metrics streams reconnect without waiting for a close handshake; late socket events are ignored.
- Temporary DSM authentication lookup failures retry without being treated as revoked sessions.
- Metric details reposition on resize and close when their widget is hidden.
- Monotonic graph timing, stream identity, and sequence numbers handle clock corrections and service restarts; reconnect gaps remain visible.
- Hidden monitors suspend their connections, and sampling pauses when no monitors are subscribed.

v1.2.0

- Fixed Escape handling so closing terminal search does not close the DSM window.
- Added the installed package version to the shell footer.
- Changed drag-and-drop uploads to use the shell's current directory without inserting file paths into the terminal.
- Added filename checks before uploading, with Override, Keep both, and Cancel choices for conflicts.
- Upload files individually; a later failure no longer deletes previously completed uploads.
- Bound upload authorization to the user, session, and checked directory; changing directories requires a new upload check.
- Hardened upload commits against directory targets and filename options, and added process timeouts and cleanup for write failures.
- Route drops to the hovered split pane, with animated target highlighting, shell name, and pane-local upload progress.
- Removed the old 100 MiB cumulative upload quota; per-file and per-request limits remain.

v1.1.1

- Added a package icon and clickable maintainer, project, and support links for Package Center.
- Corrected the package maintainer and canonical Go module path.

v1.1.0

- Added renameable shell sessions, terminal search, secure file uploads, and split views.
- Hardened uploads and polished the shell workspace controls.

v1.0.1

- Added multiple shell tabs and persistent shell sessions.

v0.1.0

- First release of DiskShell.
- Added automated SPK releases with checksums.
