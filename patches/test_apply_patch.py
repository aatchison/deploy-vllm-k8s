# SPDX-License-Identifier: Apache-2.0
import sys
from pathlib import Path
from unittest.mock import MagicMock
import pytest
import patches.apply_patch as apply_patch

def setup_mock_protocol(tmp_path, content):
    protocol_file = tmp_path / "protocol.py"
    protocol_file.write_text(content)

    # Create a mock module structure for vllm.entrypoints.anthropic.protocol
    vllm = MagicMock()
    entrypoints = MagicMock()
    anthropic = MagicMock()
    protocol = MagicMock()

    protocol.__file__ = str(protocol_file)

    vllm.entrypoints = entrypoints
    entrypoints.anthropic = anthropic
    anthropic.protocol = protocol

    import sys
    sys.modules["vllm"] = vllm
    sys.modules["vllm.entrypoints"] = entrypoints
    sys.modules["vllm.entrypoints.anthropic"] = anthropic
    sys.modules["vllm.entrypoints.anthropic.protocol"] = protocol

    return protocol_file

def test_apply_patch_success(tmp_path):
    """Verify that apply_patch successfully injects the hoist into a valid protocol.py."""
    content = (
        "from pydantic import BaseModel\n\n"
        "class AnthropicMessagesRequest(BaseModel):\n"
        '    """Anthropic Messages API request"""\n'
        "    model: str\n"
    )
    protocol_file = setup_mock_protocol(tmp_path, content)

    exit_code = apply_patch.main()
    assert exit_code == 0

    patched_text = protocol_file.read_text()
    assert apply_patch.SENTINEL in patched_text
    assert apply_patch.IMPORT_LINE in patched_text
    assert apply_patch.VALIDATOR_SRC in patched_text
    assert "class AnthropicMessagesRequest(BaseModel):" in patched_text

def test_apply_patch_idempotent(tmp_path):
    """Verify that running apply_patch twice is a no-op."""
    content = (
        "from pydantic import BaseModel\n\n"
        "class AnthropicMessagesRequest(BaseModel):\n"
        '    """Anthropic Messages API request"""\n'
        "    model: str\n"
    )
    protocol_file = setup_mock_protocol(tmp_path, content)

    apply_patch.main()
    first_run_text = protocol_file.read_text()

    exit_code = apply_patch.main()
    assert exit_code == 0

    second_run_text = protocol_file.read_text()
    assert first_run_text == second_run_text

def test_apply_patch_missing_class_anchor(tmp_path):
    """Verify that missing CLASS_ANCHOR returns 1."""
    content = '    """Anthropic Messages API request"""\n'
    setup_mock_protocol(tmp_path, content)

    exit_code = apply_patch.main()
    assert exit_code == 1

def test_apply_patch_missing_docstring_anchor(tmp_path):
    """Verify that missing DOCSTRING_ANCHOR returns 1."""
    content = "class AnthropicMessagesRequest(BaseModel):\n    pass\n"
    setup_mock_protocol(tmp_path, content)

    exit_code = apply_patch.main()
    assert exit_code == 1
