"""Preserve the complete v3 tool contract over both v4 stdio protocol eras."""

import json
import os
import sys
from pathlib import Path

import pytest
from fastmcp import Client
from fastmcp.client.transports import StdioTransport


@pytest.mark.parametrize("mode", ["auto", "legacy"])
async def test_stdio_protocol_modes_preserve_tools_and_schemas(mode, tmp_path):
    baseline = json.loads(Path(__file__).with_name("stdio_tools_baseline.json").read_text())
    expected_version = "2026-07-28" if mode == "auto" else baseline["baseline_protocol_version"]
    env = {key: value for key, value in os.environ.items() if not key.startswith("BEADS_")}
    transport = StdioTransport(
        command=sys.executable,
        args=["-m", "beads_mcp"],
        cwd=str(tmp_path),
        env=env,
    )
    async with Client(transport, mode=mode, timeout=30) as client:
        assert client.protocol_version == expected_version
        tools = await client.list_tools()
        actual = []
        for tool in tools:
            raw = tool.model_dump(by_alias=True, exclude_none=True)
            actual.append(
                {
                    key: raw[key]
                    for key in ("name", "description", "inputSchema", "outputSchema", "annotations")
                    if key in raw
                }
            )
        assert sorted(actual, key=lambda tool: tool["name"]) == baseline["tools"]
        result = await client.call_tool("discover_tools", {})
        assert not result.is_error
        assert result.data["count"] == len(result.data["tools"])
        resources = await client.list_resources()
        assert "beads://quickstart" in {str(resource.uri) for resource in resources}
