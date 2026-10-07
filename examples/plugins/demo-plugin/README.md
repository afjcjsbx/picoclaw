# Demo plugin

This Agent Plugins 1.0.0 package contains a `greet` skill and a typed MCP `greet`
tool. Build it from the PicoClaw repository root:

```sh
mkdir -p examples/plugins/demo-plugin/bin
go build -o examples/plugins/demo-plugin/bin/demo-plugin ./examples/plugins/demo-plugin
```

On Windows, use `bin/demo-plugin.exe` and update `mcp.json` accordingly.

Enable its absolute directory in `plugins.entries.demo-plugin.path`, with both
the plugin subsystem and entry enabled. Ask PicoClaw to use `demo-plugin:greet`
to greet Ada. The expected tool response is `Hello, Ada!`.

No network, credentials or external services are needed. See the [plugin guide](../../../docs/guides/plugins.md)
for the configuration example, transport rules, isolation and development guide.
