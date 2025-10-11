import { spawn } from "node:child_process";

const NODES = [
  { data: "data/data-1", port: 8081 },
  { data: "data/data-2", port: 8082 },
  { data: "data/data-3", port: 8083 },
  { data: "data/data-4", port: 8084 },
  { data: "data/data-5", port: 8085 },
];

function spawnNode(node) {
  const args = [
    "--data",
    node.data,
    "--port",
    String(node.port),
    "--nodes",
    NODES.map((x) => `127.0.0.1:${x.port}`).join(","),
  ];

  const result = {
    alive: true,
    childProcess: spawn("./raft-go", args, {
      stdio: ["ignore", "inherit", "inherit"],
    }),
    node,
  };

  result.childProcess.on("exit", (code, signal) => {
    result.alive = false;
  });

  result.childProcess.on("error", (err) => {
    result.alive = false;
    console.log(`unable to start 127.0.0.1:${node.port}`);
  });

  return result;
}

function isAlive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function killNode(obj) {
  try {
    process.kill(obj.childProcess.pid, "SIGTERM");
  } catch {}

  await new Promise((resolve) => setTimeout(resolve, 2000));

  if (isAlive(obj.childProcess.pid)) {
    try {
      process.kill(obj.childProcess.pid, "SIGKILL");
    } catch {}
  }

  await new Promise((resolve) => setTimeout(resolve, 100));
}

const arr = [];
let interval = null;

(async () => {
  for (const node of NODES) {
    const x = await spawnNode(node);

    arr.push(x);
  }

  interval = setInterval(async () => {
    const obj = arr[Math.floor(Math.random() * arr.length)];

    console.log(`[${obj.node.port}] shutting down`);
    await killNode(obj);

    await new Promise((resolve) => setTimeout(resolve, 5000));

    console.log(`[${obj.node.port}] starting`);
    await spawnNode(obj.node);
  }, 30000);
})();

process.on("SIGINT", () => {
  for (const x of arr) {
    killNode(x);
  }

  new Promise((resolve) => setTimeout(resolve, 2000)).then(() => {
    process.exit(130);
  });
});

process.on("SIGTERM", () => {
  for (const x of arr) {
    killNode(x);
  }

  new Promise((resolve) => setTimeout(resolve, 2000)).then(() => {
    process.exit(143);
  });
});

process.on("uncaughtException", async () => {
  for (const x of arr) {
    killNode(x);
  }

  new Promise((resolve) => setTimeout(resolve, 2000)).then(() => {
    process.exit(1);
  });
});

process.on("unhandledRejection", async () => {
  for (const x of arr) {
    killNode(x);
  }

  new Promise((resolve) => setTimeout(resolve, 2000)).then(() => {
    process.exit(1);
  });
});
