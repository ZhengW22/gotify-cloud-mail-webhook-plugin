#!/usr/bin/env python3
"""Load a built amd64 plugin in an isolated official Gotify Docker container.

Requires Python 3 and Docker; creates no external notifications or persistent data.
"""
import base64
import http.client
import json
import pathlib
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def main():
    root = pathlib.Path(__file__).resolve().parent.parent
    binary = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else root / "build/cloud-mail-webhook-gotify-v3.1.1-linux-amd64.so"
    if not binary.is_file():
        raise SystemExit("Build the amd64 plugin first: make build-amd64")
    name = "cloud-mail-plugin-smoke-" + secrets.token_hex(6)
    password = secrets.token_hex(24)
    authorization = "Basic " + base64.b64encode(("smoke:" + password).encode()).decode()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    container = None
    with tempfile.TemporaryDirectory(prefix="gotify-plugin-smoke-") as directory:
        shutil.copyfile(binary, pathlib.Path(directory) / "cloud-mail.so")
        try:
            container = docker(
                "run", "-d", "--name", name,
                "--platform", "linux/amd64", "-p", "127.0.0.1::80",
                "-e", "GOTIFY_DEFAULTUSER_NAME=smoke",
                "-e", "GOTIFY_DEFAULTUSER_PASS=" + password,
                "-e", "GOTIFY_PLUGINSDIR=/app/plugins",
                "-v", directory + ":/app/plugins:ro,z",
                "gotify/server:3.1.1",
            )
            info = json.loads(docker("inspect", container))[0]
            port = info["NetworkSettings"]["Ports"]["80/tcp"][0]["HostPort"]
            base = "http://127.0.0.1:" + port

            def call(path, body=None, auth=authorization, content_type="application/json"):
                headers = {"Authorization": auth, "Content-Type": content_type}
                payload = json.dumps(body).encode() if body is not None else None
                req = urllib.request.Request(base + path, data=payload, headers=headers)
                with opener.open(req, timeout=5) as response:
                    data = response.read().decode()
                    return json.loads(data) if data and response.headers.get("Content-Type", "").startswith("application/json") else data

            for attempt in range(60):
                try:
                    call("/health", auth="")
                    break
                except (urllib.error.URLError, OSError, http.client.HTTPException):
                    state = json.loads(docker("inspect", container))[0]["State"]
                    if not state["Running"]:
                        logs = docker("logs", container).replace(password, "<redacted>")
                        raise RuntimeError("Gotify exited during plugin loading:\n" + logs)
                    if attempt == 59:
                        raise RuntimeError("Gotify did not become ready; plugin load may have failed")
                    time.sleep(0.5)

            plugins = call("/plugin")
            match = next(p for p in plugins if p["modulePath"] == "gotify-cloud-mail-webhook-plugin")
            identifier = str(match["id"])
            configuration = call("/plugin/" + identifier + "/config")
            secret = re.search(r"(?m)^secret:\s*[\"']?([a-f0-9]{64})", configuration).group(1)
            call("/plugin/" + identifier + "/enable", {})
            endpoint = "/plugin/" + identifier + "/custom/" + match["token"] + "/receive"
            text_mail = {
                "emailId": 1, "subject": "纯文本测试", "sendEmail": "sender@example.com",
                "toEmail": "receiver@example.com", "text": "纯文本正文", "code": "123456",
            }
            html_mail = {
                "emailId": 2, "subject": "HTML 测试", "sendEmail": "sender@example.com",
                "content": "<h1>只有 HTML 的正文</h1><p>验证码：<b>654321</b></p><script>hidden()</script>",
            }
            assert call(endpoint, text_mail, secret)["status"] == "success"
            assert call(endpoint, text_mail, secret)["status"] == "duplicate"
            assert call(endpoint, html_mail, secret)["status"] == "success"
            try:
                call(endpoint, text_mail, "wrong-secret")
                raise AssertionError("wrong secret was accepted")
            except urllib.error.HTTPError as error:
                assert error.code == 401, error.code
            for _ in range(20):
                messages = call("/message")["messages"]
                if len(messages) == 2:
                    break
                time.sleep(0.1)
            assert len(messages) == 2, "unexpected message count"
            by_title = {m["title"]: m["message"] for m in messages}
            assert "sender@example.com" in by_title["纯文本测试"]
            assert "receiver@example.com" in by_title["纯文本测试"]
            assert "验证码：123456" in by_title["纯文本测试"]
            assert "只有 HTML 的正文" in by_title["HTML 测试"]
            assert "654321" in by_title["HTML 测试"]
            assert "hidden()" not in by_title["HTML 测试"]
            print("PASS: official Gotify 3.1.1 loads plugin; auth, text, HTML, metadata, and retry deduplication work.")
        finally:
            if container:
                # Only remove the disposable container created by this invocation.
                subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL, check=False)


if __name__ == "__main__":
    main()
