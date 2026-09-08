// Ask the reader's wallet who they are, then show them their own balance. Nothing is signed or sent.
const $ = (id) => document.getElementById(id);
const rpc = new ethers.JsonRpcProvider("https://ethereum-rpc.publicnode.com");
const key = "pledge:" + croptop.postId;
let current = null;
async function show(address) {
  current = address;
  $("wallet-connect").hidden = true; $("wallet-card").hidden = false;
  $("wallet-address").textContent = address.slice(0, 6) + "…" + address.slice(-4);
  const [name, bal] = await Promise.all([rpc.lookupAddress(address).catch(() => null), rpc.getBalance(address)]);
  if (name) $("wallet-address").textContent = name + " (" + address.slice(0, 6) + "…)";
  $("wallet-balance").textContent = Number(ethers.formatEther(bal)).toFixed(4) + " ETH on mainnet";
  const pledged = localStorage.getItem(key);
  $("wallet-pledge").textContent = pledged ? "You pledged to follow the money on " + new Date(pledged).toLocaleDateString() + "." : "";
  $("wallet-pledge-btn").hidden = !!pledged;
}
$("wallet-connect").addEventListener("click", async () => {
  if (!window.ethereum) { $("wallet-note").textContent = "No wallet found in this browser. Install one and come back."; return; }
  try { show(await croptop.wallet.connect(1)); } catch (e) { $("wallet-note").textContent = e.message; }
});
$("wallet-pledge-btn").addEventListener("click", () => { localStorage.setItem(key, new Date().toISOString()); if (current) show(current); });
