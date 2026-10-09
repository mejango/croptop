// Virtual Sepolia for the dev site. Installs a fake wallet (window.ethereum) and routes the page's
// Sepolia reads through eth_simulateV1, so "sent" transactions become simulated state that later
// reads and transactions see. Nothing is signed or broadcast. Needs a publicnode-style RPC that
// supports eth_simulateV1 and state overrides.
//
// In the dev site's console:
//   const s = document.createElement('script'); s.src = '/simchain.js'; document.head.appendChild(s);
//   sim.signer = sim.accounts[1];   // switch the "connected wallet"
//   sim.setup                       // every transaction sent so far, replayed before each call
(() => {
  const chainId = 11155111;
  const RPC = chainRpc(chainById(chainId));
  const accounts = ['0x240dc2085caef779f428dcd103cfd2fb510ede82', '0x1111111111111111111111111111111111111111'];
  const sim = window.sim = { accounts, signer: accounts[0], setup: [], txs: {}, block: 0 };
  const realFetch = window.fetch.bind(window);
  const hex = (n) => '0x' + BigInt(n).toString(16);
  const word = (n) => '0x' + BigInt(n).toString(16).padStart(64, '0');

  const rpc = async (method, params) => {
    const r = await realFetch(RPC, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) });
    const j = await r.json();
    if (j.error) throw new Error(`${method}: ${j.error.message}`);
    return j.result;
  };

  // Every simulation runs 15 minutes in the future so a freshly deployed revnet's first stage is active.
  let time;
  const simulate = async (calls) => {
    if (!time) { const b = await rpc('eth_getBlockByNumber', ['latest', false]); time = hex(parseInt(b.timestamp, 16) + 900); }
    const stateOverrides = Object.fromEntries(accounts.map(a => [a, { balance: hex(10n ** 20n) }]));
    const res = await rpc('eth_simulateV1', [{ blockStateCalls: [{ blockOverrides: { time }, stateOverrides, calls: [...sim.setup, ...calls] }], validation: false, traceTransfers: false }, 'latest']);
    const out = res[0].calls;
    const bad = out.slice(0, sim.setup.length).findIndex(c => c.status !== '0x1');
    if (bad >= 0) throw new Error(`simchain: replayed tx ${bad} failed`);
    return out.slice(sim.setup.length);
  };
  const revert = (r) => Object.assign(new Error('execution reverted'), { code: 3, data: r.error?.data || r.returnData || '0x' });

  // Reads: ethers JsonRpcProvider talks to the RPC over fetch. Route eth_call through the simulation.
  window.fetch = async (url, init) => {
    if (String(url) !== RPC || !init?.body) return realFetch(url, init);
    const body = JSON.parse(typeof init.body === 'string' ? init.body : new TextDecoder().decode(init.body)); // ethers sends bytes
    const reqs = Array.isArray(body) ? body : [body];
    const results = await Promise.all(reqs.map(async (q) => {
      if (q.method !== 'eth_call') return (await realFetch(RPC, { ...init, body: JSON.stringify(q) })).json();
      const [r] = await simulate([q.params[0]]);
      return r.status === '0x1'
        ? { jsonrpc: '2.0', id: q.id, result: r.returnData }
        : { jsonrpc: '2.0', id: q.id, error: { code: 3, message: 'execution reverted', data: r.error?.data || r.returnData || '0x' } };
    }));
    return new Response(JSON.stringify(Array.isArray(body) ? results : results[0]), { status: 200, headers: { 'content-type': 'application/json' } });
  };

  // Wallet: an EIP-1193 provider whose eth_sendTransaction is a simulation that then becomes state.
  const block = () => ({ number: hex(1000000 + sim.block), hash: word(7777 + sim.block), parentHash: word(7776 + sim.block), timestamp: time || hex(Math.floor(Date.now() / 1000)), baseFeePerGas: hex(1e9), gasLimit: hex(30000000), gasUsed: '0x0', transactions: [], miner: accounts[0], nonce: '0x0000000000000000', difficulty: '0x0', extraData: '0x', logsBloom: '0x' + '0'.repeat(512) });
  window.ethereum = {
    isSimchain: true,
    networkVersion: String(chainId),
    on() {}, removeListener() {},
    request: async ({ method, params = [] }) => {
      switch (method) {
        case 'eth_chainId': return hex(chainId);
        case 'eth_accounts': case 'eth_requestAccounts': return [sim.signer];
        case 'wallet_switchEthereumChain': case 'wallet_addEthereumChain': return null;
        case 'eth_blockNumber': return hex(1000000 + sim.block);
        case 'eth_gasPrice': case 'eth_maxPriorityFeePerGas': return hex(1e9);
        case 'eth_getBlockByNumber': return block();
        case 'eth_getTransactionCount': return hex(sim.setup.length);
        case 'eth_estimateGas': { const [r] = await simulate([params[0]]); if (r.status !== '0x1') throw revert(r); return r.gasUsed || hex(8000000); }
        case 'eth_call': { const [r] = await simulate([params[0]]); if (r.status !== '0x1') throw revert(r); return r.returnData; }
        case 'eth_sendTransaction': {
          const tx = params[0];
          const call = { from: tx.from, to: tx.to, data: tx.data || '0x', value: tx.value || '0x0' };
          const [r] = await simulate([call]);
          if (r.status !== '0x1') throw revert(r);
          sim.setup.push(call);
          sim.block++;
          const hash = ethers.keccak256(ethers.toUtf8Bytes('simchain-' + sim.setup.length));
          const b = block();
          const gasUsed = r.gasUsed || hex(8000000);
          sim.txs[hash] = {
            hash, from: tx.from, to: tx.to, input: call.data, value: call.value, nonce: hex(sim.setup.length - 1), gas: gasUsed, gasPrice: hex(1e9),
            chainId: hex(chainId), blockNumber: b.number, blockHash: b.hash, transactionIndex: '0x0', type: '0x0',
            v: '0x1b', r: word(1), s: word(1),
            receipt: {
              transactionHash: hash, transactionIndex: '0x0', blockNumber: b.number, blockHash: b.hash, from: tx.from, to: tx.to, status: '0x1',
              gasUsed, cumulativeGasUsed: gasUsed, effectiveGasPrice: hex(1e9), type: '0x0', contractAddress: null, logsBloom: '0x' + '0'.repeat(512),
              logs: r.logs.map((l, i) => ({ ...l, blockNumber: b.number, blockHash: b.hash, transactionHash: hash, transactionIndex: '0x0', logIndex: hex(i), removed: false })),
            },
          };
          return hash;
        }
        case 'eth_getTransactionByHash': return sim.txs[params[0]] || null;
        case 'eth_getTransactionReceipt': return sim.txs[params[0]]?.receipt || null;
        default: return rpc(method, params);
      }
    },
  };
  console.log('simchain: virtual Sepolia installed. sim.signer =', sim.signer);
})();
