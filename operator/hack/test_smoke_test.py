"""Offline smoke-script regression tests. All external commands are local stubs."""
import json
import os
from pathlib import Path
import subprocess


def test_smoke_script_escapes_model_id(tmp_path):
    model = 'quote" slash\\ tab\tline\nnext'
    capture = tmp_path / "requests.jsonl"
    curl = tmp_path / "curl"
    curl.write_text(r"""#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
model = os.environ["TEST_MODEL_ID"]
if "-d" not in args:
    print(json.dumps({"data": [{"id": model}]}, separators=(",", ":")))
else:
    body = json.loads(args[args.index("-d") + 1])
    assert body["model"] == model, body
    with open(os.environ["TEST_CAPTURE"], "a") as f:
        f.write(json.dumps(body) + "\n")
    print(json.dumps({"choices": [{"message": {"content": "ok"}}]}))
""")
    curl.chmod(0o755)
    kubectl = tmp_path / "kubectl"
    kubectl.write_text("""#!/usr/bin/env python3
import sys
if "-o" in sys.argv:
    print("http://offline.invalid/v1")
""")
    kubectl.chmod(0o755)
    env = dict(os.environ, PATH=str(tmp_path) + os.pathsep + os.environ["PATH"],
               TEST_MODEL_ID=model, TEST_CAPTURE=str(capture))
    script = Path(__file__).with_name("smoke-test.sh")
    result = subprocess.run(["bash", str(script), "test", "test", str(kubectl)],
                            env=env, capture_output=True, text=True, timeout=10)
    assert result.returncode == 0, result.stdout + result.stderr
    requests = [json.loads(line) for line in capture.read_text().splitlines()]
    assert len(requests) == 2
    assert all(body["model"] == model and body["max_tokens"] == 5 for body in requests)
    assert requests[0]["messages"] == [{"role": "user", "content": "Say hi"}]
    assert requests[1]["messages"] == [{"role": "system", "content": "Be brief"},
                                       {"role": "user", "content": "Say hi"}]
