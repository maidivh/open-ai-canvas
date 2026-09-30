"""Validate .env delivery without starting containers or reading deployment secrets."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class DeploymentEnvironmentTest(unittest.TestCase):
    def test_root_env_reaches_backend(self):
        root = Path(__file__).resolve().parents[4]
        values = {
            "ENABLED": "true", "AUTO_REGISTER": "true", "CLIENT_ID": "canvas-test",
            "CLIENT_SECRET": "test-only-" + "x" * 40,
            "ISSUER": "https://identity.example.com", "SITE_ID": "42",
            "AUTHORIZATION_URL": "https://design.example.com/canvas/authorize",
            "EXCHANGE_URL": "https://identity.example.com/api/ht_hot/canvas_sso/exchange",
            "REDIRECT_URL": "https://canvas.example.com/api/auth/cloooud/callback",
        }
        required = {
            "POSTGRES_PASSWORD": "test-only-password", "DATABASE_URL": "postgresql://test:test@postgres/test",
            "CANVAS_BACKEND_IMAGE": "example/backend:test", "CANVAS_WEB_IMAGE": "example/web:test",
        }
        process_env = {key: value for key, value in os.environ.items()
                       if not key.startswith(("CANVAS_", "COMPOSE_", "POSTGRES_")) and key != "DATABASE_URL"}
        with tempfile.TemporaryDirectory(prefix="canvas-sso-compose-") as directory:
            env_file = Path(directory) / ".env"
            for enabled in (True, False):
                config = dict(required)
                if enabled:
                    config.update({"CANVAS_CLOOOUD_SSO_" + key: value for key, value in values.items()})
                env_file.write_text("".join(f"{key}={value}\n" for key, value in config.items()))
                result = subprocess.run(
                    ["docker", "compose", "--env-file", str(env_file), "-f", str(root / "docker-compose.deploy.yml"),
                     "config", "--format", "json"],
                    cwd=root, env=process_env, capture_output=True, text=True, check=True,
                )
                services = json.loads(result.stdout)["services"]
                backend = services["backend"]["environment"]
                if enabled:
                    for key, value in values.items():
                        self.assertEqual(str(backend.get("CANVAS_CLOOOUD_SSO_" + key)), value, key)
                else:
                    self.assertEqual(str(backend.get("CANVAS_CLOOOUD_SSO_ENABLED")), "false")
                    self.assertEqual(str(backend.get("CANVAS_CLOOOUD_SSO_AUTO_REGISTER")), "false")
                for name in ("web", "migrate", "postgres", "redis"):
                    self.assertNotIn("CANVAS_CLOOOUD_SSO_CLIENT_SECRET", services[name].get("environment", {}))


if __name__ == "__main__":
    unittest.main()
