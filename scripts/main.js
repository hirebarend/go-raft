let node = "localhost:8081";

async function send() {
  console.log(`[${node}] -> request`);

  const response = await fetch(`http://${node}/propose`, {
    method: "POST",
  });

  if (response.status !== 200) {
    throw new Error(`expected 200, got ${response.status}`);
  }

  const json = await response.json();

  if (node !== json.leader_id) {
    node = json.leader_id;
  }
}

(async () => {
  await send();

  for (let i = 0; i < 10; i++) {
    const promises = new Array(30).fill(0).map(() => send());

    await Promise.all(promises);
  }
})();
