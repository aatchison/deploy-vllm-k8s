# SPDX-License-Identifier: Apache-2.0
"""Bake the Anthropic system-role hoist into the installed vLLM at build time.

Run inside the image build (see the root ``Dockerfile``). This:

  1. Copies ``system_hoist.py`` next to vLLM's anthropic ``protocol.py`` as
     ``_system_hoist.py`` (a private sibling module).
  2. Edits ``protocol.py`` to import it and register a
     ``@model_validator(mode="before")`` on ``AnthropicMessagesRequest`` that
     calls ``hoist_system_messages``.

The validator must live in the class body (not a post-hoc monkeypatch) because
pydantic-core builds the validation schema at class-definition time and
FastAPI validates via that compiled schema, bypassing Python-level overrides.

Design goals:
  * Idempotent — re-running (or rebuilding a layer) is a no-op.
  * Fail loud — if the expected anchors are gone (upstream nightly drift), the
    build aborts with a clear message rather than silently shipping the bug.
"""

import shutil
import sys
from pathlib import Path

SENTINEL = "_hoist_system_messages"  # marks an already-patched protocol.py

# Inserted at module scope, immediately before the target class.
IMPORT_LINE = "from vllm.entrypoints.anthropic._system_hoist import hoist_system_messages\n"

CLASS_ANCHOR = "class AnthropicMessagesRequest(BaseModel):"
DOCSTRING_ANCHOR = '    """Anthropic Messages API request"""'

# Inserted as the first method of AnthropicMessagesRequest, right after its
# docstring. mode="before" runs on the raw dict ahead of the strict per-message
# Literal["user","assistant"] check, so hoisted system entries are removed
# before that validator ever sees them.
VALIDATOR_SRC = '''
    @model_validator(mode="before")
    @classmethod
    def _hoist_system_messages(cls, data):
        # Claude Code (and some other clients) place the system prompt as a
        # role="system" entry inside messages[]. Anthropic's schema only allows
        # user/assistant there and expects system as a top-level field. Hoist
        # such entries into top-level `system` before validation. See
        # patches/system_hoist.py (baked in as _system_hoist.py).
        return hoist_system_messages(data)
'''


def main() -> int:
    import vllm.entrypoints.anthropic.protocol as protocol_mod

    protocol_path = Path(protocol_mod.__file__)
    pkg_dir = protocol_path.parent

    # 1. Drop the pure-logic sibling module beside protocol.py.
    src = Path(__file__).with_name("system_hoist.py")
    shutil.copyfile(src, pkg_dir / "_system_hoist.py")

    text = protocol_path.read_text()

    if SENTINEL in text:
        print(f"[apply_patch] {protocol_path} already patched; nothing to do.")
        return 0

    if CLASS_ANCHOR not in text:
        print(
            f"[apply_patch] ERROR: anchor {CLASS_ANCHOR!r} not found in "
            f"{protocol_path}. vLLM's Anthropic protocol changed — review the "
            "patch before shipping.",
            file=sys.stderr,
        )
        return 1
    if DOCSTRING_ANCHOR not in text:
        print(
            f"[apply_patch] ERROR: anchor {DOCSTRING_ANCHOR!r} not found in "
            f"{protocol_path}. vLLM's Anthropic protocol changed — review the "
            "patch before shipping.",
            file=sys.stderr,
        )
        return 1

    # 2a. Add the import just before the class definition.
    text = text.replace(CLASS_ANCHOR, IMPORT_LINE + "\n\n" + CLASS_ANCHOR, 1)

    # 2b. Insert the validator immediately after the class docstring.
    text = text.replace(
        DOCSTRING_ANCHOR, DOCSTRING_ANCHOR + "\n" + VALIDATOR_SRC, 1
    )

    protocol_path.write_text(text)
    print(f"[apply_patch] patched {protocol_path}: system-role hoist installed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
