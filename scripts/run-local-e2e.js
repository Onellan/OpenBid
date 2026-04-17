const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const repoRoot = path.resolve(__dirname, "..");
const composeFile = path.join(repoRoot, "ProductionDeployment", "docker-compose.yml");
const runtimeDataDir = path.join(repoRoot, "ProductionDeployment", "runtime", "data");
const e2eDbPath = path.join(runtimeDataDir, "e2e.db");
const smokeDbPath = path.join(runtimeDataDir, "local-smoke.db");

function shouldUseShell(command) {
  return process.platform === "win32" && (command === "npx" || command === "npm");
}

function quoteForCmd(value) {
  if (!/[\s"]/u.test(value)) {
    return value;
  }
  return `"${value.replace(/"/g, '\\"')}"`;
}

function run(command, args, env) {
  const spawnOptions = {
    cwd: repoRoot,
    env: { ...process.env, ...env },
    stdio: "inherit",
  };
  const result = shouldUseShell(command)
    ? spawnSync(process.env.ComSpec || "cmd.exe", ["/d", "/s", "/c", [command, ...args].map(quoteForCmd).join(" ")], spawnOptions)
    : spawnSync(command, args, spawnOptions);
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    process.exit(result.status || 1);
  }
}

function removeIfPresent(filePath) {
  try {
    fs.rmSync(filePath, { force: true });
  } catch (error) {
    if (error && error.code !== "ENOENT") {
      throw error;
    }
  }
}

if (!fs.existsSync(smokeDbPath)) {
  console.error(`missing healthy e2e baseline database: ${smokeDbPath}`);
  process.exit(1);
}

run("docker", ["compose", "-f", composeFile, "stop", "proxy", "app", "worker"]);

fs.copyFileSync(smokeDbPath, e2eDbPath);
removeIfPresent(`${e2eDbPath}-wal`);
removeIfPresent(`${e2eDbPath}-shm`);

run("go", ["run", "./cmd/e2e_seed"], {
  DATA_PATH: e2eDbPath,
});

run(
  "docker",
  [
    "compose",
    "-f",
    composeFile,
    "up",
    "-d",
    "--build",
    "--wait",
    "--force-recreate",
    "app",
    "worker",
    "proxy",
  ],
  {
    APP_ENV: "development",
    DATA_PATH: "/app/data/e2e.db",
    SECURE_COOKIES: "false",
  },
);

run("npx", ["playwright", "test"]);