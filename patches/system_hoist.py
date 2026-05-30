# SPDX-License-Identifier: Apache-2.0
"""System-role hoisting for vLLM's Anthropic Messages endpoint.

vLLM's native ``/v1/messages`` handler validates each entry in ``messages[]``
against ``Literal["user", "assistant"]`` and expects the system prompt as a
*top-level* ``system`` field. Some clients — notably Claude Code — instead emit
the system prompt as a ``role="system"`` entry **inside** ``messages[]``, which
the strict validator rejects with HTTP 400::

    {'type': 'literal_error', 'loc': ('body', 'messages', N, 'role'),
     'msg': "Input should be 'user' or 'assistant'", 'input': 'system'}

This module hoists any such entries out of ``messages[]`` and into the
top-level ``system`` field before per-message validation runs. It is wired into
``AnthropicMessagesRequest`` as a ``@model_validator(mode="before")`` at image
build time (see ``patches/apply_patch.py``), so the strict ``AnthropicMessage``
validator never sees the system entries.

The logic is pure (stdlib only, plain dicts in / plain dict out) so it can be
unit-tested without importing vLLM — see ``patches/test_system_hoist.py``.
"""


def anthropic_text(content) -> str:
    """Flatten Anthropic content (``str | list[block] | None``) to plain text.

    Content blocks contribute their ``text`` field; bare strings pass through.
    Non-text blocks (images, tool_use, ...) are dropped — only textual system
    instructions are meaningful as a hoisted system prompt.
    """
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for block in content:
            if isinstance(block, dict):
                text = block.get("text")
                if text:
                    parts.append(text)
            elif isinstance(block, str):
                parts.append(block)
        return "\n\n".join(parts)
    return str(content)


def hoist_system_messages(data):
    """Pull ``role="system"`` entries out of ``messages[]`` into ``system``.

    Order is preserved and any pre-existing top-level ``system`` content is kept
    by prepending it ahead of the hoisted entries. Returns ``data`` unchanged
    when there is nothing to hoist (or the input is not the expected shape), so
    it is safe as an unconditional ``mode="before"`` validator.
    """
    if not isinstance(data, dict):
        return data
    messages = data.get("messages")
    if not isinstance(messages, list):
        return data

    hoisted = []
    kept = []
    for message in messages:
        if isinstance(message, dict) and message.get("role") == "system":
            hoisted.append(anthropic_text(message.get("content")))
        else:
            kept.append(message)

    if not hoisted:
        return data

    parts = []
    existing = data.get("system")
    if existing is not None:
        parts.append(anthropic_text(existing))
    parts.extend(hoisted)

    # Copy rather than mutate the caller's dict; merge to a single string, which
    # the serving layer accepts for the top-level system field.
    patched = dict(data)
    patched["messages"] = kept
    patched["system"] = "\n\n".join(part for part in parts if part)
    return patched
