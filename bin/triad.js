#!/usr/bin/env node

const { spawn } = require("child_process");
const path = require("path");

const platform = process.platform; // win32, darwin, linux
const arch = process.arch;          // x64, arm64

let binaryName = "";

if (platform === "win32" && arch === "x64") {
  binaryName = "triad-win-x64.exe";
} else if (platform === "darwin" && arch === "arm64") {
  binaryName = "triad-darwin-arm64";
} else if (platform === "darwin" && arch === "x64") {
  binaryName = "triad-darwin-x64";
} else if (platform === "linux" && arch === "x64") {
  binaryName = "triad-linux-x64";
} else {
  console.error(`Unsupported platform/architecture: ${platform}-${arch}`);
  process.exit(1);
}

const binaryPath = path.join(__dirname, "..", "dist", binaryName);

const child = spawn(binaryPath, process.argv.slice(2), {
  stdio: "inherit"
});

child.on("exit", (code) => {
  process.exit(code ?? 0);
});

child.on("error", (err) => {
  console.error("Failed to start triad binary:", err);
  process.exit(1);
});
