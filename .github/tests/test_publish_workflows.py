"""Static publish contracts; registry/OIDC execution is merge-only."""
from pathlib import Path
import re
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github/workflows"


def load(name):
    # BaseLoader keeps GitHub's YAML 1.2 `on` key and scalar strings intact.
    return yaml.load((WORKFLOWS / name).read_text(), Loader=yaml.BaseLoader)


class PublishContracts(unittest.TestCase):
    def jobs(self):
        for name, job in (("operator-image.yaml", "publish"),
                          ("release.yaml", "release-image")):
            yield name, load(name)["jobs"][job]

    def test_main_only_publish_requires_ci(self):
        workflow = load("operator-image.yaml")
        self.assertIn("main", workflow["on"]["push"]["branches"])
        self.assertIn("pull_request", workflow["on"])
        job = workflow["jobs"]["publish"]
        self.assertEqual(job["needs"], "ci")
        self.assertEqual(job["if"],
                         "github.event_name == 'push' && github.ref == 'refs/heads/main'")
        self.assertEqual(load("release.yaml")["on"]["push"]["tags"], ["v*"])

    def test_all_actions_are_sha_pinned(self):
        for path in WORKFLOWS.glob("*.yaml"):
            for job in load(path.name)["jobs"].values():
                for step in job["steps"]:
                    if "uses" in step:
                        self.assertRegex(step["uses"], r"^[^@]+@[0-9a-f]{40}$")

    def test_ghcr_and_ephemeral_credentials(self):
        for name, job in self.jobs():
            workflow = load(name)
            self.assertEqual(workflow["env"]["REGISTRY"], "ghcr.io")
            self.assertEqual(workflow["env"]["IMAGE_NAME"],
                             "${{ github.repository_owner }}/vllm-operator")
            self.assertEqual(workflow["permissions"], {"contents": "read"})
            self.assertEqual(job["permissions"]["packages"], "write")
            self.assertEqual(job["permissions"]["id-token"], "write")
            login = next(s for s in job["steps"]
                         if s.get("uses", "").startswith("docker/login-action@"))
            self.assertEqual(login["with"]["password"], "${{ secrets.GITHUB_TOKEN }}")
            secrets = re.findall(r"secrets\.([A-Za-z_][A-Za-z_0-9]*)",
                                 (WORKFLOWS / name).read_text())
            self.assertEqual(secrets, ["GITHUB_TOKEN"])

    def test_build_publishes_sbom_and_provenance_without_latest(self):
        for _, job in self.jobs():
            build = next(s for s in job["steps"] if s.get("id") == "build")
            self.assertEqual(build["with"]["context"], "operator")
            self.assertEqual(build["with"]["file"], "operator/Dockerfile")
            for key in ("push", "sbom", "provenance"):
                self.assertEqual(build["with"][key], "true")
            metadata = next(s for s in job["steps"] if s.get("id") == "meta")
            self.assertEqual(metadata["with"]["flavor"], "latest=false")

    def test_digest_scan_blocks_signing(self):
        for _, job in self.jobs():
            steps = job["steps"]
            scan = next(s for s in steps
                        if s.get("uses", "").startswith("aquasecurity/trivy-action@"))
            sign = next(s for s in steps if "cosign sign" in s.get("run", ""))
            self.assertLess(steps.index(scan), steps.index(sign))
            self.assertEqual(scan["with"]["image-ref"],
                "${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}@${{ steps.build.outputs.digest }}")
            self.assertEqual(scan["with"]["exit-code"], "1")
            self.assertEqual(scan["with"]["severity"], "CRITICAL,HIGH")
            self.assertEqual(scan["with"]["ignore-unfixed"], "true")
            self.assertEqual(scan["with"]["vuln-type"], "os,library")
            for step in (scan, sign):
                self.assertNotIn("continue-on-error", step)
                self.assertNotIn("if", step)  # default success(), never always()
            self.assertEqual(sign["env"]["DIGEST"], "${{ steps.build.outputs.digest }}")
            self.assertEqual(sign["run"], 'cosign sign --yes "${IMAGE}@${DIGEST}"')

    def test_summary_is_after_signing_and_prints_digest(self):
        for _, job in self.jobs():
            steps = job["steps"]
            summary = next(s for s in steps
                           if "GITHUB_STEP_SUMMARY" in s.get("run", ""))
            sign = next(s for s in steps if "cosign sign" in s.get("run", ""))
            self.assertGreater(steps.index(summary), steps.index(sign))
            self.assertNotIn("if", summary)
            self.assertEqual(summary["env"]["DIGEST"], "${{ steps.build.outputs.digest }}")
            self.assertIn("${IMAGE}@${DIGEST}", summary["run"])
            self.assertIn('test -n "$DIGEST"', summary["run"])

    def test_pr_runs_contracts_without_publish_permissions(self):
        job = load("pr-validate.yaml")["jobs"]["workflow-contracts"]
        self.assertNotIn("if", job)
        self.assertNotIn("permissions", job)
        self.assertTrue(any("unittest discover -s .github/tests" in s.get("run", "")
                            for s in job["steps"]))


if __name__ == "__main__":
    unittest.main()
