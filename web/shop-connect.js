/* global ethers, CHAINS, getProvider, ensureWalletOnChain */
const env = {};
(() => {
 'use strict';
 const $ = id => document.getElementById(id);
 const [siteID, chainText, sessionID, secret] = location.hash.slice(1).split('/');
 const chainID = Number(chainText), network = CHAINS.find(c => c.id === chainID);
 const key = `croptop-connect:${sessionID}`;
 if (secret) sessionStorage.setItem(key, secret);
 const token = secret || sessionStorage.getItem(key);
 history.replaceState(null, '', `/shop-connect#${siteID}/${chainID}/${sessionID}`);
 const endpoint = `/v0/croptop/sites/${encodeURIComponent(siteID)}/shop-connect/${chainID}`;
 let session, busy = false, reviewed = false;
 const message = text => { $('message').textContent = text; };
 const c = () => session.connection;
 async function api(action, extra = {}) {
  const r = await fetch(action ? `${endpoint}/${sessionID}` : endpoint, { method: action ? 'POST' : 'GET', cache:'no-store', headers: {'Content-Type':'application/json','X-Croptop-Shop':'1','X-Shop-Token':token}, ...(action ? {body:JSON.stringify({action,...extra})} : {}) });
  if (!r.ok) { let reason = await r.text(); try {reason = JSON.parse(reason).error || reason;} catch {} throw new Error(reason); }
  session = (await r.json()).session;
  if (session?.id !== sessionID || session.siteId !== siteID || c()?.chainId !== chainID) throw new Error('Open shop setup again from site settings.');
 }
 function render() {
  if (!session) return;
  $('setup').hidden = false;
  const connection = c(), state = connection.state, tx = connection.transaction, attempt = tx?.attempt;
  $('settings-link').href = `/#/site/${siteID}/settings`;
  $('shop-target').textContent = `${network.label}: ${connection.hook}\nCategory ${connection.category}${state ? `, project ${state.projectId}` : ''}`;
  $('permission-status').textContent = state?.publisherGranted ? 'Croptop already has permission to add posts.' : 'The shop owner must allow Croptop to add posts.';
  $('price-unit').textContent = state?.currency === 1 ? 'Ξ' : state?.currency === 2 ? 'USD' : state ? `Currency ${state.currency}` : '';
  const rules = connection.expected || state?.criteria;
  if (rules) {
   const configured = rules.minimumTotalSupply > 0;
   const minimum = configured ? rules.minimumTotalSupply : 1, maximum = configured ? rules.maximumTotalSupply : 0, split = configured ? rules.maximumSplitPercent / 10000000 : 10;
   const allowed = configured ? rules.allowedAddresses : [];
   $('posting-rules').textContent = `Copies per post: ${minimum} to ${maximum || 'unlimited'}. Posters may keep up to ${split}% of sales. ${allowed.length ? `Allowed posters: ${allowed.join(', ')}.` : 'Anyone may add posts.'}${configured ? ' These rules stay the same when you change the price.' : ' These rules will be set for this category.'}`;
  }
  const active = attempt && !['confirmed','reverted','replaced'].includes(attempt.state);
  $('connect-price').disabled = busy || !!active || connection.completed;
  $('review-setup').hidden = !!active || connection.completed;
  $('review-setup').disabled = busy || !state;
  $('setup-review').hidden = !reviewed || connection.completed;
  $('approve-setup').disabled = busy;
  $('setup-progress').hidden = !attempt;
  $('transaction-status').textContent = ({signing:'Wallet result unknown. Check its transaction history before retrying.',cancelled:'Wallet approval cancelled.',pending:'Waiting for network finality.',confirmed:'Transaction confirmed.'})[attempt?.state] || '';
  if (attempt?.hash && document.activeElement !== $('setup-hash')) $('setup-hash').value = attempt.hash;
  $('retry-setup').hidden = !['signing','cancelled'].includes(attempt?.state);
  $('retry-setup').disabled = busy;
  $('check-setup').disabled = busy || !attempt;
  $('finish-setup').hidden = !connection.expected || !!tx && !(tx.kind === 'criteria' && attempt?.state === 'confirmed') || connection.completed;
  $('finish-setup').disabled = busy;
  if (connection.completed) { $('connect-details').hidden = true; $('setup-progress').hidden = true; message('Shop connected. Publish your site when you’re ready.'); }
 }
 async function run(fn) {
  if (busy) return; busy = true; render();
  try { await fn(); } catch (e) { reviewed = false; message(e.shortMessage || e.message || 'Please try again.'); }
  finally { busy = false; render(); }
 }
 async function wallet() {
  if (!window.ethereum) throw new Error('Open this page in a browser with a wallet extension.');
  await window.ethereum.request({method:'eth_requestAccounts'}); await ensureWalletOnChain(chainID);
  const provider = new ethers.BrowserProvider(window.ethereum);
  if (Number((await provider.getNetwork()).chainId) !== chainID) throw new Error('Switch your wallet to this shop’s network.');
  return {provider, signer:await provider.getSigner()};
 }
 async function estimate(from) {
  const tx = c().transaction;
  if (!tx) return;
  const provider = getProvider(chainID);
  if (Number((await provider.getNetwork()).chainId) !== chainID) throw new Error('RPC network mismatch.');
  const request = {from,to:tx.to,data:tx.data,value:0n};
  await provider.call(request);
  const gas = await provider.estimateGas(request), fee = await provider.getFeeData();
  const price = fee.maxFeePerGas || fee.gasPrice;
  if (!price) throw new Error('Could not estimate the network fee.');
  $('setup-cost').textContent = `Estimated network fee: ${ethers.formatEther(gas * price)} ETH. Your wallet shows the final fee.`;
  $('step-description').textContent = tx.kind === 'permission' ? 'Allow Croptop to add posts to this project. Its existing permissions will be retained. You’ll review the posting rules next.' : `Set the minimum post price to ${$('connect-price').value} ${c().state.currency === 1 ? 'ETH' : c().state.currency === 2 ? 'USD' : `units of currency ${c().state.currency}`} for category ${c().category}.`;
  reviewed = true;
 }
 async function review(saved = false) {
  reviewed = false;
  const {signer} = await wallet(), from = await signer.getAddress();
  if (!saved) {
   await api('plan',{from,price:$('connect-price').value.trim()});
   sessionStorage.removeItem(`${key}:hash`); $('setup-hash').value='';
  }
  if (!c().transaction) { await api('finish'); return; }
  await estimate(from); message('Review this step, then approve it in your wallet.');
 }
 async function check() {
  const hash = $('setup-hash').value.trim() || c().transaction?.attempt?.hash || sessionStorage.getItem(`${key}:hash`) || '';
  if (!hash) throw new Error('Enter the transaction hash from your wallet, or review the saved transaction to retry with the same nonce.');
  await api('verify',{hash});
  if (c().transaction?.attempt?.state === 'confirmed') {
   if (c().transaction.kind === 'criteria') await api('finish');
   else message('Permission confirmed. Review setup to continue with the posting rules.');
  } else if (!c().transaction) message('The transaction reverted or was replaced. Review setup to try again.');
  else message('Transaction checked. Waiting for network finality.');
 }
 $('connect-details').onsubmit = e => {e.preventDefault(); run(() => review());};
 $('retry-setup').onclick = () => run(() => review(true));
 $('check-setup').onclick = () => run(check);
 $('finish-setup').onclick = () => run(() => api('finish'));
 $('connect-price').oninput = () => {reviewed=false; render();};
 $('approve-setup').onclick = () => run(async () => {
  if (!reviewed || !c().transaction) return;
  const {provider,signer} = await wallet(), from = await signer.getAddress();
  await estimate(from);
  const old = c().transaction.attempt;
  if (old && !['signing','cancelled'].includes(old.state)) throw new Error('Check the saved transaction first.');
  const nonce = old?.nonce || await provider.send('eth_getTransactionCount',[from,'pending']);
  if (!Number.isSafeInteger(Number(BigInt(nonce)))) throw new Error('The wallet returned an unsupported transaction nonce.');
  await api('signing',{from,nonce});
  const saved = c().transaction;
  let tx;
  try {tx = await signer.sendTransaction({to:saved.to,data:saved.data,value:0n,nonce:Number(BigInt(nonce))});}
  catch(e) {if (e.code === 'ACTION_REJECTED' || e.code === 4001) await api('cancelled'); throw e;}
  sessionStorage.setItem(`${key}:hash`,tx.hash); $('setup-hash').value = tx.hash;
  await api('submitted',{hash:tx.hash}); reviewed=false;
  message('Transaction submitted. This page keeps your progress; confirmation waits for network finality.');
 });
 run(async () => {
  if (!network || !token) throw new Error('Open shop setup from site settings.');
  await api();
  if (!c().completed) await api('inspect');
  const state = c().state;
  if (state) $('connect-price').value = c().price || (state.criteria.minimumTotalSupply ? ethers.formatUnits(state.criteria.minimumPrice,state.decimals) : state.decimals >= 3 ? '0.001' : '1');
  $('setup-hash').value = c().transaction?.attempt?.hash || sessionStorage.getItem(`${key}:hash`) || '';
  message('');
 });
 setInterval(() => {
  if (!busy && session && !c().completed && c().transaction?.attempt?.state === 'pending') run(check);
 },12000);
})();
