import { test } from "node:test";
import assert from "node:assert/strict";
import { generateKeyPairSync, sign } from "node:crypto";
import { publicKeyOf, verify, base36Decode } from "../src/index.js";

// build a k51 name the way kubo does: base36 of CIDv1(libp2p-key, identity mh(protobuf pubkey))
function ipnsName(pub) {
  const proto = Uint8Array.from([0x08, 0x01, 0x12, 0x20, ...pub]);
  const cid = Uint8Array.from([0x01, 0x72, 0x00, proto.length, ...proto]);
  let n = 0n; for (const b of cid) n = (n << 8n) | BigInt(b);
  let s = ""; const A = "0123456789abcdefghijklmnopqrstuvwxyz";
  while (n > 0n) { s = A[Number(n % 36n)] + s; n /= 36n; }
  return "k" + s;
}

test("public key round-trips through a k51 name", () => {
  const { publicKey } = generateKeyPairSync("ed25519");
  const raw = new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32));
  const name = ipnsName(raw);
  assert.ok(name.startsWith("k51"), name);
  assert.deepEqual(publicKeyOf(name), raw);
});

test("a signature by the key inside the name verifies", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const raw = new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32));
  const name = ipnsName(raw);
  const msg = "croptop-push\nlocalhost\n" + name + "\nbafy\n3\n1";
  const sig = sign(null, Buffer.from(msg), privateKey).toString("base64");
  assert.equal(await verify(name, msg, sig), true);
  assert.equal(await verify(name, msg + "x", sig), false);
});

test("FOLLO's real name decodes to a 32 byte key", () => {
  const k = publicKeyOf("k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g");
  assert.equal(k && k.length, 32);
  console.log("FOLLO pubkey", Buffer.from(k).toString("hex"));
});
