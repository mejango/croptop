// No real wallet or blockchain transactions. The API and injected wallet are fixtures.
import {test} from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=fileURLToPath(new URL('../',import.meta.url));
const site='11111111-1111-1111-1111-111111111111',id='a'.repeat(64),token='b'.repeat(64),wallet='0x1111111111111111111111111111111111111111';
const mock=`
window.signatures=[];window.sent=[];window.rejectSignature=!sessionStorage.getItem('test-rejected');window.rejectPayment=true;
window.ethereum={networkVersion:'1',async request({method,params}){if(method==='eth_requestAccounts')return ['${wallet}'];if(method==='wallet_switchEthereumChain'){this.networkVersion=String(Number(params[0].chainId));return null;}throw new Error(method);}};
ethers.BrowserProvider=class{async getNetwork(){return {chainId:BigInt(window.ethereum.networkVersion)};}async send(){return '0x5';}async getSigner(){return {getAddress:async()=>'${wallet}',signTypedData:async(domain,types,message)=>{if(window.rejectSignature && window.signatures.length===1){window.rejectSignature=false;sessionStorage.setItem('test-rejected','1');throw Object.assign(new Error('Signature rejected'),{code:'ACTION_REJECTED'});}window.signatures.push({domain,types,message});return '0x'+'ab'.repeat(65);},sendTransaction:async tx=>{if(window.rejectPayment){window.rejectPayment=false;throw Object.assign(new Error('Payment rejected'),{code:'ACTION_REJECTED'});}window.sent.push(tx);return {hash:'0x'+'c'.repeat(64)};}};}};
`;
test('one Relayr payment sets up four networks with signature and payment recovery',async()=>{
 const chains=[1,10,42161,8453].map(chainId=>({connection:{chainId,hook:'0x'+'2'.repeat(40),category:1},requests:[],status:'Ready to review'}));
 const bundle={chains:[],completed:false};const session={id,siteId:site,bundle},actions=[],errors=[],external=[];let checks=0;
 const server=http.createServer(async(req,res)=>{
  if(req.url.startsWith('/v0/')){
   assert.equal(req.headers['x-shop-token'],token);assert.equal(req.headers['x-croptop-shop'],'1');
   if(req.method==='POST'){
    let raw='';for await(const chunk of req)raw+=chunk;const a=JSON.parse(raw);actions.push(a);
    if(a.action==='resolve'){assert.equal(a.chain,1);assert.equal(a.projectID,'42');bundle.chains=chains;bundle.resolved=true;}
    if(a.action==='plan'){
     bundle.from=a.from;bundle.price=a.price;bundle.deadline=Math.floor(Date.now()/1000)+3600;
     for(const chain of chains){const c=chain.connection;c.state={currency:1,decimals:18,projectId:'42'};c.expected={minimumPrice:'5000000000000000',minimumTotalSupply:2,maximumTotalSupply:50,maximumSplitPercent:75000000,allowedAddresses:[]};chain.requests=(c.chainId===1?['permission','criteria']:['criteria']).map((kind,index)=>({from:wallet,to:'0x'+'3'.repeat(40),value:'0',gas:'300000',nonce:String(7+index),deadline:bundle.deadline,data:'0x12345678',kind}));}
    }
    if(a.action==='signature'){chains.find(c=>c.connection.chainId===a.chain).requests[a.index].signature=a.signature;}
    if(a.action==='quote'){assert.ok(chains.every(c=>c.requests.every(r=>r.signature)));bundle.quote={bundle_uuid:'12345678-1234-1234-1234-123456789abc',payment_info:[1,10,42161,8453].map(chain=>({chain,amount:'0x5af3107a4000',target:'0x'+'4'.repeat(40),calldata:'0x12345678'}))};}
    if(a.action==='paying'){bundle.payment=bundle.quote.payment_info.find(p=>p.chain===a.chain);bundle.attempt={from:a.from,nonce:a.nonce,state:'signing'};}
    if(a.action==='cancelled'){delete bundle.attempt;delete bundle.payment;}
    if(a.action==='submitted'){bundle.attempt.hash=a.hash;bundle.attempt.state='pending';}
    if(a.action==='check'){checks++;for(const chain of chains){chain.ready=checks>1||chain.connection.chainId!==1;chain.status=chain.ready?'Posting setup confirmed':'Waiting for posting permissions and rules to be confirmed';}if(checks>1)bundle.completed=true;}
   }
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify({session}));return;
  }
  const path=req.url==='/shop-setup'?'web/shop-setup.html':req.url.startsWith('/fonts/')?'templates/croptop/assets/'+req.url.slice(7):req.url.startsWith('/shop-assets/')?'templates/croptop/assets/'+req.url.slice(13):'web'+req.url;
  try{let body=await readFile(root+path);if(path.endsWith('ethers.umd.min.js'))body=Buffer.concat([body,Buffer.from('\n'+mock)]);res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':path.endsWith('.css')?'text/css':path.endsWith('.png')?'image/png':path.endsWith('.woff2')?'font/woff2':'text/html');res.end(body);}catch{res.statusCode=404;res.end();}
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});
 try{
  const page=await browser.newPage({viewport:{width:1100,height:850}});page.setDefaultTimeout(10000);page.on('pageerror',e=>errors.push(e.message));await page.route('https://**',r=>{external.push(r.request().url());r.abort();});
  await page.goto(`http://127.0.0.1:${server.address().port}/shop-setup#${site}/${id}/${token}`);
  assert.equal(await page.locator('#networks').isVisible(),false);assert.equal(await page.locator('#setup-form').isVisible(),false);assert.equal(await page.getByText(/Relayr/).count(),0);await page.screenshot({path:'/tmp/croptop-project-lookup.png',fullPage:true});await page.locator('#project-id').fill('42');await page.locator('#find-project').click();await page.locator('#setup-form:not([hidden])').waitFor();
  await page.screenshot({path:'/tmp/croptop-project-resolved.png',fullPage:true});
  await page.locator('#setup-price').fill('0.005');await page.locator('#review').click();await page.locator('#sign').click();await page.getByText('Signature rejected',{exact:true}).waitFor();
  assert.equal(actions.filter(a=>a.action==='signature').length,1);
  await page.reload();await page.locator('#sign').click();await page.locator('#funding:not([hidden])').waitFor();
  assert.equal(actions.filter(a=>a.action==='signature').length,5);assert.equal(actions.filter(a=>a.action==='plan').length,1);
  const signed=await page.evaluate(()=>window.signatures);assert.deepEqual(signed.map(s=>s.domain.chainId),[1,10,42161,8453]);assert.equal(signed[0].message.nonce,'8');assert.equal(signed[0].domain.name,'Juicebox');assert.equal(signed[0].domain.verifyingContract,'0x3ba60b60933916a7c87d0860dcee62a0ce34e3e2');
  await page.locator('#funding-chain').selectOption('8453');await page.locator('#pay').click();await page.getByText('Payment rejected',{exact:true}).waitFor();assert.equal(bundle.attempt,undefined);
  await page.locator('#pay').click();await page.locator('#progress:not([hidden])').waitFor();
  assert.equal(actions.filter(a=>a.action==='submitted').length,1);assert.deepEqual(actions.filter(a=>a.action==='paying').map(a=>a.chain),[8453,8453]);assert.equal(await page.evaluate(()=>window.sent.length),1);
  await page.reload();await page.locator('#check').click();await page.getByText('Waiting for posting permissions and rules to be confirmed',{exact:true}).waitFor();assert.equal(bundle.completed,false);assert.equal(await page.locator('#pay').isVisible(),false);
  await page.screenshot({path:'/tmp/croptop-bundle-desktop.png',fullPage:true});await page.setViewportSize({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:'/tmp/croptop-bundle-mobile.png',fullPage:true});
  await page.locator('#check').click();await page.getByText('Posting is set up on all networks. Publish your site when you’re ready.',{exact:true}).waitFor();
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});
