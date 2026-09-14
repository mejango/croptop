/* global ethers, CHAINS, buildRevnetDeployArgs, croptopRevnetStages, revnetDeployerContract, creationFeeOn, REV_DEPLOYER_ABI, ensureWalletOnChain, getProvider, SPLITS_TOTAL_PERCENT */
const env = {}; // Pinned network defaults; no site-authored scripts or RPC settings.
(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const [siteID, sessionID, fragmentToken] = location.hash.slice(1).split('/');
  const tokenKey = `croptop-shop:${sessionID}`;
  if (fragmentToken) sessionStorage.setItem(tokenKey, fragmentToken);
  const token = fragmentToken || sessionStorage.getItem(tokenKey);
  history.replaceState(null, '', `/shop#${siteID}/${sessionID}`);
  let session, busy = false, reviewed = new Set();
  const endpoint = `/v0/croptop/sites/${encodeURIComponent(siteID)}/shop`;
  const message = text => { $('message').textContent = text; };
  const el = (tag, text, className) => { const n = document.createElement(tag); if (text) n.textContent = text; if (className) n.className = className; return n; };
  const actionButton = (text, fn) => { const b = el('button', text, 'btn'); b.type = 'button'; b.disabled = busy; b.onclick = () => run(fn); return b; };
  async function api(action, extra = {}) {
    const response = await fetch(action ? `${endpoint}/${sessionID}` : endpoint, {
      method: action ? 'POST' : 'GET', cache: 'no-store',
      headers: { 'Content-Type': 'application/json', 'X-Croptop-Shop': '1', 'X-Shop-Token': token },
      ...(action ? { body: JSON.stringify({ action, ...extra }) } : {})
    });
    if (!response.ok) { let reason = await response.text(); try { reason = JSON.parse(reason).error || reason; } catch {} throw new Error(reason); }
    session = (await response.json()).session;
    if (!session || session.id !== sessionID || session.siteId !== siteID) throw new Error('This shop session is no longer available. Open it from site settings.');
    return session;
  }
  async function run(fn) {
    if (busy) return;
    busy = true; document.querySelectorAll('button').forEach(b => { b.disabled = true; });
    try { await fn(); } catch (error) { message(error.shortMessage || error.message || 'Please try again.'); }
    finally { busy = false; if (session) render(); document.querySelectorAll('button').forEach(b => { b.disabled = false; }); updateQuote(); }
  }
  function updateQuote() {
    const form = $('details');
    const price = form.elements.price.value.trim();
    $('get-quote').disabled = busy || !form.checkValidity() || !form.querySelector('[name=chain]:checked') ||
      !/^(0|[1-9][0-9]{0,10})(\.[0-9]{1,18})?$/.test(price);
  }
  function networkOptions() {
    const testnet = $('environment').value === 'testnet';
    $('networks').replaceChildren(...CHAINS.filter(c => c.testnet === testnet).map(c => {
      const label = el('label', '', 'network'), input = document.createElement('input');
      input.type = 'checkbox'; input.name = 'chain'; input.value = c.id; input.checked = c.id === (testnet ? 84532 : 8453);
      label.append(input, document.createTextNode(c.label)); return label;
    }));
    updateQuote();
  }
  function deployment(config) {
    return { ...config, salt: session.salt, stages: croptopRevnetStages(), postingRules: [
      { category: 0, minimumPrice: 0n, minimumTotalSupply: 1n, maximumTotalSupply: 0n, maximumSplitPercent: SPLITS_TOTAL_PERCENT, allowedAddresses: [config.owner] },
      { category: 1, minimumPrice: ethers.parseEther(config.price), minimumTotalSupply: 1n, maximumTotalSupply: 0n, maximumSplitPercent: SPLITS_TOTAL_PERCENT / 10, allowedAddresses: [] }
    ] };
  }
  async function wallet(chainID) {
    if (!window.ethereum) throw new Error('Open this page in a browser with an Ethereum wallet extension.');
    await window.ethereum.request({ method: 'eth_requestAccounts' });
    await ensureWalletOnChain(chainID);
    const provider = new ethers.BrowserProvider(window.ethereum);
    if (Number((await provider.getNetwork()).chainId) !== chainID) throw new Error('Switch your wallet to the selected network.');
    return { provider, signer: await provider.getSigner() };
  }
  async function estimate(chain, from) {
    const provider = getProvider(chain.id);
    if (Number((await provider.getNetwork()).chainId) !== chain.id) throw new Error('RPC network mismatch.');
    if ((await provider.getCode(revnetDeployerContract())) === '0x') throw new Error('The v6 deployer is unavailable on this network.');
    const fee = await creationFeeOn(chain.id);
    if (fee !== BigInt(chain.value)) throw new Error('The creation fee changed. This saved transaction cannot be signed at the old fee.');
    const tx = { from, to: revnetDeployerContract(), data: chain.data, value: BigInt(chain.value) };
    // Both simulation and estimation must succeed before a wallet send is offered.
    await provider.call(tx);
    const gas = await provider.estimateGas(tx), fees = await provider.getFeeData();
    const gasPrice = fees.maxFeePerGas || fees.gasPrice;
    if (!gasPrice) throw new Error('Could not estimate the network fee.');
    return { fee, gas: gas * gasPrice };
  }
  async function review() {
    if (!session.config) {
      const form = new FormData($('details'));
      const config = { name: form.get('name').trim(), symbol: form.get('symbol').trim(), owner: ethers.getAddress(form.get('owner').trim()), price: form.get('price').trim(), chainIds: form.getAll('chain').map(Number) };
      if (!config.chainIds.length) throw new Error('Select a network.');
      if (!/^(0|[1-9][0-9]{0,10})(\.[0-9]{1,18})?$/.test(config.price)) throw new Error('Enter a price in ETH with at most 18 decimals.');
      const input = deployment(config), iface = new ethers.Interface(REV_DEPLOYER_ABI);
      const chains = await Promise.all(config.chainIds.map(async id => ({ id, data: iface.encodeFunctionData('deployFor', buildRevnetDeployArgs(input, id, session.startsAt)), value: ethers.toQuantity(await creationFeeOn(id)), attempts: [] })));
      await api('plan', { config, chains });
    }
    const estimates = [];
    reviewed.clear();
    for (const chain of session.chains.filter(c => !c.address && c.attempts.at(-1)?.state !== 'pending')) {
      message(`Estimating ${CHAINS.find(c => c.id === chain.id).label}…`);
      const { signer } = await wallet(chain.id);
      const currentFee = ethers.toQuantity(await creationFeeOn(chain.id));
      if (BigInt(currentFee) !== BigInt(chain.value)) {
        await api('reprice', { chainId: chain.id, value: currentFee });
        chain.value = currentFee;
      }
      const cost = await estimate(chain, await signer.getAddress());
      estimates.push(el('p', `${CHAINS.find(c => c.id === chain.id).label}: ${ethers.formatEther(cost.fee)} ETH creation fee + up to ${ethers.formatEther(cost.gas)} ETH estimated gas.`));
      reviewed.add(chain.id);
    }
    $('costs').replaceChildren(...estimates);
    $('review').hidden = !reviewed.size;
    $('summary').textContent = `${session.config.name} (${session.config.symbol}); Revenue wallet ${session.config.owner}; Minimum post price ${session.config.price} ETH`;
    message(reviewed.size ? 'Review the cost, then approve in your wallet.' : 'Check the saved transactions below.');
  }
  async function send(chain) {
    const { provider, signer } = await wallet(chain.id);
    const from = await signer.getAddress();
    await estimate(chain, from); // Recheck immediately before each signature.
    const previous = chain.attempts.at(-1);
    const retry = ['signing', 'cancelled'].includes(previous?.state);
    if (retry && from.toLowerCase() !== previous.from.toLowerCase()) throw new Error('Resume with the wallet that approved this network.');
    const nonce = retry ? previous.nonce : await provider.send('eth_getTransactionCount', [from, 'pending']);
    await api('signing', { chainId: chain.id, from, nonce });
    message(`Approve ${CHAINS.find(c => c.id === chain.id).label} in your wallet.`);
    let tx;
    try { tx = await signer.sendTransaction({ to: revnetDeployerContract(), data: chain.data, value: BigInt(chain.value), nonce: Number(BigInt(nonce)) }); }
    catch (error) {
      // Only an explicit wallet rejection proves that no transaction was sent.
      if (error.code === 'ACTION_REJECTED' || error.code === 4001) await api('cancelled', { chainId: chain.id });
      else message('The wallet result is uncertain. Use its transaction hash below to recover before trying again.');
      throw error;
    }
    // Save a local backup first, covering a node/network failure after broadcast.
    sessionStorage.setItem(`${tokenKey}:${chain.id}:hash`, tx.hash);
    await api('submitted', { chainId: chain.id, hash: tx.hash });
    reviewed.delete(chain.id);
  }
  async function check(chain, hash) {
    await api('verify', { chainId: chain.id, hash: hash || chain.attempts.at(-1)?.hash || sessionStorage.getItem(`${tokenKey}:${chain.id}:hash`) || '' });
    const current = session.chains.find(c => c.id === chain.id);
    message(current.applied ? 'Shop saved. Publish your site when you’re ready.' : 'Progress saved. Pending transactions are checked until finalized.');
  }
  function render() {
    $('details').hidden = !!session.config;
    $('progress').hidden = !session.config;
    $('edit').hidden = !session.config || session.chains.some(c => c.attempts.length);
    $('settings-link').href = `/#/site/${siteID}/settings`;
    if (!session.config) return;
    const labels = { signing: 'Wallet result unknown', pending: 'Waiting for network finality', confirmed: 'Confirmed', reverted: 'Transaction reverted', replaced: 'Transaction replaced', cancelled: 'Signing cancelled' };
    $('chains').replaceChildren(...session.chains.map(chain => {
      const box = el('div', '', 'chain'), last = chain.attempts.at(-1);
      box.append(el('strong', CHAINS.find(c => c.id === chain.id).label), el('p', chain.applied ? 'Saved to shop settings' : (labels[last?.state] || 'Ready to review')));
      if (chain.address) box.append(el('p', chain.address));
      if (last?.hash) box.append(el('p', `Transaction: ${last.hash}`));
      if (last?.state === 'signing' || last?.state === 'pending' || (chain.address && !chain.applied)) {
        const input = document.createElement('input'); input.placeholder = 'Transaction hash from your wallet'; input.setAttribute('aria-label', 'Transaction hash'); input.value = last?.hash || sessionStorage.getItem(`${tokenKey}:${chain.id}:hash`) || '';
        box.append(input, actionButton('Check transaction', () => check(chain, input.value.trim())));
        if (last?.state === 'signing') box.append(el('p', 'If the app closed during approval, check your wallet history. Review remaining networks to resume with the same wallet and nonce, or enter its transaction hash.', 'help'));
      }
      return box;
    }));
    const saved = session.chains.filter(c => c.applied).length;
    $('publish-note').textContent = saved ? `${saved} of ${session.chains.length} networks saved. Publish from site settings when you’re ready.` : 'Your progress is saved on this node.';
    if (session.chains.some(c => !c.address && c.attempts.at(-1)?.state !== 'pending') && !$('resume-review')) {
      const b = actionButton('Review remaining networks', review); b.id = 'resume-review'; $('progress').append(b);
    } else if (!session.chains.some(c => !c.address && c.attempts.at(-1)?.state !== 'pending')) $('resume-review')?.remove();
    if (!reviewed.size) $('review').hidden = true;
  }
  $('edit').onclick = () => run(async () => {
    const config = session.config;
    await api('edit'); reviewed.clear(); $('review').hidden = true;
    for (const key of ['name', 'symbol', 'owner', 'price']) $('details').elements[key].value = config[key];
    message('Update your shop details, then review again.');
  });
  $('connect-wallet').onclick = () => run(async () => {
    if (!window.ethereum) throw new Error('Open this page in a browser with an Ethereum wallet extension.');
    const accounts = await window.ethereum.request({ method: 'eth_requestAccounts' });
    if (!accounts?.length) throw new Error('No wallet address was connected.');
    if (!$('details').elements.owner.value.trim()) $('details').elements.owner.value = ethers.getAddress(accounts[0]);
    message('Wallet connected.');
  });
  $('details').addEventListener('input', updateQuote);
  $('details').addEventListener('change', updateQuote);
  $('environment').onchange = networkOptions;
  $('details').onsubmit = e => { e.preventDefault(); run(review); };
  $('approve').onclick = () => run(async () => {
    for (const id of [...reviewed]) { await send(session.chains.find(c => c.id === id)); render(); }
    message('Transactions submitted. You can return to settings; this session keeps your progress.');
  });
  networkOptions();
  run(async () => {
    if (!token) throw new Error('Open shop creation from site settings.');
    await api();
    $('details').elements.name.value = session.siteName;
    message(session.config ? 'Your saved deployment is ready to resume.' : '');
  });
  setInterval(() => {
    if (busy || !session?.config) return;
    const pending = session.chains.filter(c => !c.applied && (c.attempts.at(-1)?.hash || sessionStorage.getItem(`${tokenKey}:${c.id}:hash`)) && ['signing', 'pending', 'confirmed'].includes(c.attempts.at(-1)?.state));
    if (pending.length) run(async () => { for (const c of pending) await check(c); });
  }, 12000);
})();
