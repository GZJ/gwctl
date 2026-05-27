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
