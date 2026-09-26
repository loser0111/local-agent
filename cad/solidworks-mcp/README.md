# SolidWorks MCP Server

A [Model Context Protocol](https://modelcontextprotocol.io) server for SolidWorks automation via win32com.

## Installation

### Prerequisites

- SolidWorks 2022+ installed on Windows
- Python 3.9+
- pywin32 (`pip install pywin32`)
- mcp (`pip install mcp`)

### Setup

```bash
# Install dependencies
pip install pywin32 mcp

# Verify SolidWorks COM connectivity
python sw_com_check.py
```

### Claude Desktop Configuration

Add this to your Claude Desktop config file:

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "solidworks": {
      "command": "python",
      "args": ["C:\\path\\to\\sw_mcp_server.py"]
    }
  }
}
```

## Tools

### `sw_create_part`
Create a new part document.

**Parameters:**
- `template` (optional): "mm" for metric (default), "in" for imperial

### `sw_sketch_rectangle`
Draw a rectangle in a sketch.

**Parameters:**
- `width` (required): Width in meters
- `height` (required): Height in meters

### `sw_sketch_circle`
Draw a circle in a sketch.

**Parameters:**
- `radius` (required): Radius in meters

### `sw_extrude`
Extrude the current sketch.

**Parameters:**
- `depth` (required): Extrusion depth in meters
- `thin_feature` (optional): Create thin feature (default: false)

### `sw_extrude_cut`
Cut extrude from the current sketch.

**Parameters:**
- `depth` (required): Cut depth in meters

### `sw_save`
Save the active document.

**Parameters:**
- `path` (optional): Full path to save to (defaults to a new document name)

### `sw_get_info`
Get information about the active document.

### `sw_close`
Close the active document.

**Parameters:**
- `save` (optional): Save before closing (default: true)

## Key Concepts

- **Units**: SolidWorks API uses meters. 1mm = 0.001m, 1 inch = 0.0254m
- **Coordinate System**: Origin at (0,0,0), X-right, Y-up, Z-out-of-screen
- **Sketches**: Must be in sketch edit mode. Use `sw_create_rectangle`, `sw_create_circle` etc.
- **Features**: Created from sketches. Extrude, cut, revolve, etc.

## License

MIT
