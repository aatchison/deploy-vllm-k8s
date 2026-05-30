# SPDX-License-Identifier: Apache-2.0
"""Unit tests for the Anthropic system-role hoist logic.

Pure-logic tests — no vLLM import required. Run with::

    python -m pytest patches/test_system_hoist.py
    # or, without pytest installed:
    python patches/test_system_hoist.py
"""

from system_hoist import anthropic_text, hoist_system_messages


def test_system_in_messages_is_hoisted():
    """The core fix: role="system" inside messages[] -> top-level system."""
    data = {
        "model": "m",
        "max_tokens": 16,
        "messages": [
            {"role": "system", "content": "You are terse."},
            {"role": "user", "content": "hi"},
        ],
    }
    out = hoist_system_messages(data)
    assert out["system"] == "You are terse."
    assert out["messages"] == [{"role": "user", "content": "hi"}]
    # No system role survives in messages[] (would otherwise 400).
    assert all(m["role"] != "system" for m in out["messages"])


def test_multiple_system_messages_concatenated_in_order():
    data = {
        "messages": [
            {"role": "system", "content": "first"},
            {"role": "user", "content": "hi"},
            {"role": "system", "content": "second"},
        ]
    }
    out = hoist_system_messages(data)
    assert out["system"] == "first\n\nsecond"
    assert out["messages"] == [{"role": "user", "content": "hi"}]


def test_existing_top_level_system_is_preserved_and_prepended():
    data = {
        "system": "existing",
        "messages": [
            {"role": "system", "content": "hoisted"},
            {"role": "user", "content": "hi"},
        ],
    }
    out = hoist_system_messages(data)
    assert out["system"] == "existing\n\nhoisted"


def test_system_content_as_block_list_is_flattened():
    data = {
        "messages": [
            {"role": "system", "content": [{"type": "text", "text": "block one"},
                                            {"type": "text", "text": "block two"}]},
            {"role": "user", "content": "hi"},
        ]
    }
    out = hoist_system_messages(data)
    assert out["system"] == "block one\n\nblock two"


def test_no_system_message_is_unchanged():
    data = {"messages": [{"role": "user", "content": "hi"}]}
    out = hoist_system_messages(data)
    assert out == data


def test_non_dict_input_passes_through():
    assert hoist_system_messages(None) is None
    assert hoist_system_messages("nope") == "nope"


def test_anthropic_text_variants():
    assert anthropic_text(None) == ""
    assert anthropic_text("plain") == "plain"
    assert anthropic_text([{"type": "text", "text": "a"}, {"type": "image"}]) == "a"


if __name__ == "__main__":
    fns = [v for k, v in sorted(globals().items()) if k.startswith("test_")]
    for fn in fns:
        fn()
        print(f"ok  {fn.__name__}")
    print(f"\n{len(fns)} passed")
