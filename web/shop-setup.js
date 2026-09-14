/* global ethers, CHAINS, ensureWalletOnChain */
const env = {};
(() => {
 'use strict';
 const $=id=>document.getElementById(id);
 const [siteID,sessionID,secret]=location.hash.slice(1).split('/');
 const key=`croptop-setup:${sessionID}`;
 if(secret) sessionStorage.setItem(key,secret);
 const token=secret||sessionStorage.getItem(key);
 history.replaceState(null,'',`/shop-setup#${siteID}/${sessionID}`);
 const endpoint=`/v0/croptop/sites/${encodeURIComponent(siteID)}/shop-setup`;
 const FORWARDER='0x3ba60b60933916a7c87d0860dcee62a0ce34e3e2';
 const types={ForwardRequest:[{name:'from',type:'address'},{name:'to',type:'address'},{name:'value',type:'uint256'},{name:'gas',type:'uint256'},{name:'nonce',type:'uint256'},{name:'deadline',type:'uint48'},{name:'data',type:'bytes'}]};
 let session,busy=false,selectionDirty=false;
 const bundle=()=>session?.bundle;
 const label=id=>CHAINS.find(c=>c.id===id)?.label||String(id);
 const message=text=>{$('message').textContent=text;};
 async function api(action,extra={}) {
  const r=await fetch(action?`${endpoint}/${sessionID}`:endpoint,{method:action?'POST':'GET',cache:'no-store',headers:{'Content-Type':'application/json','X-Croptop-Shop':'1','X-Shop-Token':token},...(action?{body:JSON.stringify({action,...extra})}:{})});
  if(!r.ok){let text=await r.text();try{text=JSON.parse(text).error||text;}catch{}throw new Error(text.replace(/Relayr/gi,'network fee service'));}
  session=(await r.json()).session;
  if(session?.id!==sessionID||session.siteId!==siteID||!Array.isArray(bundle()?.chains))throw new Error('Open posting setup again from settings.');
 }
 function render() {
  const b=bundle();if(!b)return;
  $('setup').hidden=false;
  $('settings-link').href=`/#/site/${siteID}/settings`;
  const requests=b.chains.flatMap(c=>c.requests||[]),unsigned=requests.some(r=>!r.signature),planned=!!b.deadline;
  const active=planned&&!b.completed;
  const resolved=(b.resolved||planned)&&!selectionDirty;
  $('project-form').hidden=planned||b.completed;
  $('find-project').disabled=busy;
  $('project-chain').disabled=busy;
  $('project-id').disabled=busy;
  $('setup-form').hidden=!resolved||b.completed;
  $('networks').hidden=!resolved;
  $('project-result').hidden=!resolved;
  $('project-result').textContent=`Found your shop on ${b.chains.map(c=>label(c.connection.chainId)).join(', ')}. Review the networks and posting rules below.`;
  $('setup-price').disabled=busy||active;
  $('review').hidden=active&&Date.now()<b.deadline*1000;
  $('review').disabled=busy;
  $('authorization').hidden=!active||!unsigned||!!b.attempt;
  $('sign').disabled=busy;
  $('sign').textContent=requests.some(r=>r.signature)?'Continue signing':'Sign setup requests';
  $('quote').hidden=!active||unsigned||!requests.length||!!b.attempt;
  $('quote').disabled=busy;
  $('quote').textContent=b.quote?'Refresh network fee quote':'Get network fee quote';
  $('funding').hidden=!b.quote||!!b.attempt||b.completed;
  const select=$('funding-chain'),selected=select.value;
  select.replaceChildren(...(b.quote?.payment_info||[]).map(p=>{const o=document.createElement('option');o.value=p.chain;o.textContent=label(p.chain);return o;}));
  if([...select.options].some(o=>o.value===selected))select.value=selected;
  select.disabled=busy;
  showCost();$('pay').disabled=busy;
  $('progress').hidden=!b.attempt||b.completed;
  $('payment-status').textContent=({signing:'Wallet result unknown. Check your wallet history before retrying the saved payment.',pending:'Funding payment submitted. Setting up the destination networks.',confirmed:'Funding confirmed. Waiting for posting setup on each network.',reverted:'The funding transaction reverted. Check the destination networks before reviewing again.',replaced:'The funding transaction was replaced. Check the destination networks before reviewing again.'})[b.attempt?.state]||'';
  if(b.attempt?.hash && document.activeElement!==$('funding-hash'))$('funding-hash').value=b.attempt.hash;
  $('retry-payment').hidden=b.attempt?.state!=='signing';$('retry-payment').disabled=busy;
  $('check').hidden=!planned||b.completed;$('check').disabled=busy;
  $('networks').replaceChildren(...b.chains.map(chain=>{
   const c=chain.connection,section=document.createElement('section'),title=document.createElement('h2'),target=document.createElement('p'),status=document.createElement('p');
   title.textContent=label(c.chainId);target.className='help';target.textContent=`${c.hook}${c.state?`\nProject ${c.state.projectId}, category ${c.category}`:''}`;status.textContent=chain.status;
   section.append(title,target,status);
   const criteria=c.expected||c.state?.criteria;
   if(criteria){const rules=document.createElement('p');rules.className='help';rules.textContent=`Minimum price: ${ethers.formatUnits(criteria.minimumPrice,c.state.decimals)} ${c.state.currency===1?'ETH':c.state.currency===2?'USD':`currency ${c.state.currency}`}. Copies per post: ${criteria.minimumTotalSupply} to ${criteria.maximumTotalSupply||'unlimited'}. Posters may keep up to ${criteria.maximumSplitPercent/10000000}% of sales. ${criteria.allowedAddresses.length?`Allowed posters: ${criteria.allowedAddresses.join(', ')}.`:'Anyone may add posts.'}`;const details=document.createElement('details'),summary=document.createElement('summary');summary.textContent='Posting rules';details.append(summary,rules);section.append(details);}
   if(chain.requests?.length){const steps=document.createElement('p');steps.className='help';steps.textContent=chain.requests.map(r=>`${r.kind==='permission'?'Grant Croptop posting permission':'Set posting allowances'}${r.signature?' (signed)':''}`).join('. ');section.append(steps);}
   return section;
  }));
  const currency=b.chains[0]?.connection.state?.currency;$('price-unit').textContent=currency===2?'USD':currency&&currency!==1?`Currency ${currency}`:'Ξ';
  if(!resolved)$('networks').replaceChildren();
  if(b.completed)message('Posting is set up on all networks. Publish your site when you’re ready.');
 }
 function showCost(){const p=bundle()?.quote?.payment_info.find(p=>p.chain===Number($('funding-chain').value));$('funding-cost').textContent=p?`${ethers.formatEther(p.amount)} ETH covers the destination networks. Your wallet also shows the funding transaction’s network fee.`:'';}
 async function run(fn) {
  if(busy)return;
  const work=async()=>{busy=true;render();try{await fn();}catch(e){message(e.shortMessage||e.message||'Please try again.');}finally{busy=false;render();}};
  if(navigator.locks)await navigator.locks.request(`croptop-posting:${siteID}`,{ifAvailable:true},async lock=>{if(lock)await work();else message('Posting setup is active in another tab. Continue there.');});else await work();
 }
 async function wallet(chain, requireSaved = true) {
  if(!window.ethereum)throw new Error('Open this page in a browser with a wallet extension.');
  await window.ethereum.request({method:'eth_requestAccounts'});
  if(chain)await ensureWalletOnChain(chain);
  const provider=new ethers.BrowserProvider(window.ethereum),signer=await provider.getSigner(),from=await signer.getAddress();
  if(chain && Number((await provider.getNetwork()).chainId)!==chain)throw new Error('Switch to the requested network.');
  if(requireSaved&&bundle()?.from&&bundle().from.toLowerCase()!==from.toLowerCase())throw new Error('Use the wallet that reviewed this setup.');
  return{provider,signer,from};
 }
 async function check(){await api('check',{hash:$('funding-hash').value.trim()||bundle().attempt?.hash||sessionStorage.getItem(`${key}:hash`)||''});if(!bundle().completed)message('Checked the contracts on every network. Confirmed setup is listed below.');}
 for(const c of CHAINS){const option=document.createElement('option');option.value=c.id;option.textContent=c.label;$('project-chain').append(option);}
 $('project-chain').onchange=$('project-id').oninput=()=>{selectionDirty=true;render();};
 $('project-form').onsubmit=e=>{e.preventDefault();run(async()=>{message('Finding your shop and its linked networks…');await api('resolve',{chain:Number($('project-chain').value),projectID:$('project-id').value.trim()});selectionDirty=false;const c=bundle().chains[0].connection; $('setup-price').value=c.state?.criteria.minimumTotalSupply?ethers.formatUnits(c.state.criteria.minimumPrice,c.state.decimals):'0.001';message('');});};
 $('setup-form').onsubmit=e=>{e.preventDefault();run(async()=>{const {from}=await wallet(undefined, false);message('Checking permissions and preparing setup on every network…');await api('plan',{from,price:$('setup-price').value.trim()});sessionStorage.removeItem(`${key}:hash`);$('funding-hash').value='';if(!bundle().chains.some(c=>c.requests.length))await check();else message('Review the networks and posting rules, then sign the setup requests.');});};
 $('sign').onclick=()=>run(async()=>{
  await api();
  for(const chain of bundle().chains){
   for(let index=0;index<chain.requests.length;index++){
    const request=chain.requests[index];if(request.signature)continue;
    message(`Sign ${request.kind==='permission'?'the posting permission':'the posting allowances'} on ${label(chain.connection.chainId)}.`);
    const {signer}=await wallet(chain.connection.chainId);
    const {from,to,value,gas,nonce,deadline,data}=request;
    const backup=`${key}:signature:${chain.connection.chainId}:${index}:${deadline}:${nonce}`;
    let signature=sessionStorage.getItem(backup);
    if(!signature){signature=await signer.signTypedData({name:'Juicebox',version:'1',chainId:chain.connection.chainId,verifyingContract:FORWARDER},types,{from,to,value,gas,nonce,deadline,data});sessionStorage.setItem(backup,signature);}
    await api('signature',{chain:chain.connection.chainId,index,signature});render();
   }
  }
  message('Signatures saved. Getting a network fee quote…');await api('quote');message('Choose one network to fund setup across all the destination networks.');
 });
 $('quote').onclick=()=>run(async()=>{message('Getting a network fee quote…');await api('quote');message('Choose where to pay the network fees.');});
 $('funding-chain').onchange=showCost;
 async function pay(saved=false){
  const chain=saved?bundle().payment.chain:Number($('funding-chain').value);
  const {provider,signer,from}=await wallet(chain);
  const nonce=saved?bundle().attempt.nonce:await provider.send('eth_getTransactionCount',[from,'pending']);
  if(!Number.isSafeInteger(Number(BigInt(nonce))))throw new Error('Unsupported wallet nonce.');
  await api('paying',{from,nonce,chain});
  const p=bundle().payment;
  let tx;
  try{tx=await signer.sendTransaction({to:p.target,data:p.calldata,value:BigInt(p.amount),nonce:Number(BigInt(nonce))});}
  catch(e){if(e.code==='ACTION_REJECTED'||e.code===4001)await api('cancelled');throw e;}
  sessionStorage.setItem(`${key}:hash`,tx.hash);$('funding-hash').value=tx.hash;
  await api('submitted',{hash:tx.hash});message('Payment submitted. We’ll set up posting across the networks. You can return to this page to check progress.');
 }
 $('pay').onclick=()=>run(()=>pay());$('retry-payment').onclick=()=>run(()=>pay(true));$('check').onclick=()=>run(check);
 run(async()=>{if(!token)throw new Error('Open posting setup from site settings.');await api();$('project-chain').value=String(bundle().sourceChain||1);$('project-id').value=bundle().sourceProject||'';$('setup-price').value=bundle().price||'0.001';$('funding-hash').value=bundle().attempt?.hash||sessionStorage.getItem(`${key}:hash`)||'';message(bundle().deadline?'Your saved setup is ready to continue.':'Enter a network and project ID to find your shop and its linked networks.');});
 setInterval(()=>{if(!busy&&bundle()?.attempt&&!bundle().completed&&bundle().attempt.state!=='signing')run(check);},15000);
})();
