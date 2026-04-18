const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const repoRoot = path.resolve(__dirname, "..");
const composeFile = path.join(repoRoot, "ProductionDeployment", "docker-compose.yml");
const runtimeDataDir = path.join(repoRoot, "ProductionDeployment", "runtime", "data");
const e2eDbPath = path.join(runtimeDataDir, "e2e.db");
const smokeDbPath = path.join(runtimeDataDir, "local-smoke.db");
const storeDbPath = path.join(runtimeDataDir, "store.db");

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
    const error = new Error(`command failed: ${command} ${args.join(" ")}`);
    error.exitCode = result.status || 1;
    throw error;
  }
}

function canRun(command, args, env) {
  const spawnOptions = {
    cwd: repoRoot,
    env: { ...process.env, ...env },
    stdio: "ignore",
  };
  const result = shouldUseShell(command)
    ? spawnSync(process.env.ComSpec || "cmd.exe", ["/d", "/s", "/c", [command, ...args].map(quoteForCmd).join(" ")], spawnOptions)
    : spawnSync(command, args, spawnOptions);
  return !result.error && result.status === 0;
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

function cleanupE2eArtifacts() {
  removeIfPresent(e2eDbPath);
  removeIfPresent(`${e2eDbPath}-wal`);
  removeIfPresent(`${e2eDbPath}-shm`);

  return [e2eDbPath, `${e2eDbPath}-wal`, `${e2eDbPath}-shm`].filter((p) => fs.existsSync(p));
}

function ensureNoSeededE2eData() {
  let leftovers = cleanupE2eArtifacts();
  if (leftovers.length === 0) {
    return;
  }

  // Fallback for Windows file locking edge-cases: keep the path but reset it to a clean baseline.
  if (!fs.existsSync(smokeDbPath)) {
    throw new Error(`failed to clean e2e artifacts and missing fallback baseline: ${leftovers.join(", ")}`);
  }
  fs.copyFileSync(smokeDbPath, e2eDbPath);
  removeIfPresent(`${e2eDbPath}-wal`);
  removeIfPresent(`${e2eDbPath}-shm`);
  leftovers = [`${e2eDbPath}-wal`, `${e2eDbPath}-shm`].filter((p) => fs.existsSync(p));
  if (leftovers.length > 0) {
    throw new Error(`failed to clean e2e sqlite sidecars: ${leftovers.join(", ")}`);
  }
}

function resolveRestoreDbPath() {
  if (fs.existsSync(storeDbPath) && canRun("go", ["run", "./cmd/sqlite_check"], { DATA_PATH: storeDbPath })) {
    return { hostPath: storeDbPath, containerPath: "/app/data/store.db" };
  }
  if (fs.existsSync(smokeDbPath) && canRun("go", ["run", "./cmd/sqlite_check"], { DATA_PATH: smokeDbPath })) {
    return { hostPath: smokeDbPath, containerPath: "/app/data/local-smoke.db" };
  }
  throw new Error("no healthy non-e2e database found (checked store.db and local-smoke.db)");
}

function restoreLocalStackToStoreDb() {
  const restoreDb = resolveRestoreDbPath();
  console.log(`restoring localhost to non-e2e database: ${restoreDb.hostPath}`);

  run("docker", ["compose", "-f", composeFile, "stop", "proxy", "app", "worker"]);
  ensureNoSeededE2eData();

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
      DATA_PATH: restoreDb.containerPath,
      SECURE_COOKIES: "false",
    },
  );

  ensureNoSeededE2eData();
}

function main() {
  if (!fs.existsSync(smokeDbPath)) {
    throw new Error(`missing healthy e2e baseline database: ${smokeDbPath}`);
  }

  run("docker", ["compose", "-f", composeFile, "stop", "proxy", "app", "worker"]);

  ensureNoSeededE2eData();
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
  restoreLocalStackToStoreDb();
}

try {
  main();
} catch (error) {
  console.error(error.message || error);
  process.exit(error.exitCode || 1);
}
