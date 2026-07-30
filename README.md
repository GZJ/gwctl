# gwctl

Suite of Window Management Tools

## Install

Install all commands:

```bash
go install github.com/GZJ/gwctl/cmd/...@latest
```

Install a single command:

```bash
go install github.com/GZJ/gwctl/cmd/gwc-tray@latest
```

## Compatibility

Most `gwc-*` commands in this repository are currently Windows-only.

The Linux `gwc-tray` implementation is built on X11 (`xgb` / `xproto`) and does not currently support Wayland.

## gwc-tray usage

Control one window whose title is exactly `Alacritty`:

```bash
gwc-tray -title "Alacritty"
```

Add a global shortcut for toggling that window:

```bash
gwc-tray -title "Alacritty" -key "ctrl+shift+a"
```

Alternatively, control one specific window by its decimal or hexadecimal X11
window ID:

```bash
gwc-tray -id 0x1234567 -key "ctrl+shift+a"
```

Specify multiple targets by repeating and mixing `-title` and `-id`:

```bash
gwc-tray \
  -title "Editor" \
  -title "Terminal" \
  -id 0x1234567 \
  -key "ctrl+shift+a"
```

Each `-title` resolves one exactly matching window. All targets are toggled as
one group. They are hidden in command-line order and shown in reverse order, so
the first target is shown last and becomes the frontmost window.

## Test

Run the test suite with:

```bash
make test
```

The multi-window integration test uses Alacritty as its test application. It
runs automatically when both an X11 display and Alacritty are available, and is
otherwise skipped.
