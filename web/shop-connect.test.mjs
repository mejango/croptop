// Wallet and chain responses are mocked. This never submits a blockchain transaction.
import {test} from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright');
const root=fileURLToPath(new URL('../',import.meta.url));
const site='11111111-1111-1111-1111-111111111111',id='a'.repeat(64),token='b'.repeat(64),wallet='0x1111111111111111111111111111111111111111';
const mock=`
window.sent=[];window.rejectNext=true;
window.ethereum={networkVersion:'8453',async request({method}){if(method==='eth_requestAccounts')return ['${wallet}'];throw new Error(method);}};
class Provider{async getNetwork(){return {chainId:8453n};}async call(){return '0x';}async estimateGas(){return 100000n;}async getFeeData(){return {maxFeePerGas:1000000000n};}}
ethers.JsonRpcProvider=Provider;
ethers.BrowserProvider=class extends Provider{async send(){return '0x5';}async getSigner(){return {getAddress:async()=>'${wallet}',sendTransaction:async tx=>{if(window.rejectNext){window.rejectNext=false;throw Object.assign(new Error('Rejected'),{code:'ACTION_REJECTED'});}window.sent.push(tx);return {hash:'0x'+'c'.repeat(64)};}};}};
`;
for(const revnet of [false,true]) test(`existing shop wallet flow, revnet=${revnet}`,async()=>{
 const state={owner:wallet,projectId:'42',currency:1,decimals:18,publisherGranted:revnet,canConfigure:true,canGrant:true,criteria:{minimumPrice:'1000000000000000',minimumTotalSupply:2,maximumTotalSupply:50,maximumSplitPercent:75000000,allowedAddresses:[wallet]}};
 const connection={chainId:8453,hook:'0x'+'2'.repeat(40),category:1,state,completed:false};
 const session={id,siteId:site,connection},actions=[],external=[],errors=[];
 const server=http.createServer(async(req,res)=>{
  if(req.url.startsWith('/v0/')){
   assert.equal(req.headers['x-shop-token'],token);assert.equal(req.headers['x-croptop-shop'],'1');
   if(req.method==='POST'){
    let raw='';for await(const p of req)raw+=p;const a=JSON.parse(raw);actions.push(a);
    if(a.action==='plan'){
     connection.price=a.price;connection.expected={...state.criteria,minimumPrice:'5000000000000000'};
     connection.transaction=state.publisherGranted?{kind:'criteria',to:'0x'+'3'.repeat(40),data:'0x12345678'}:{kind:'permission',to:'0x'+'4'.repeat(40),data:'0x12345678'};
    }
    if(a.action==='signing')connection.transaction.attempt={from:a.from,nonce:a.nonce,state:'signing'};
    if(a.action==='cancelled')connection.transaction.attempt.state='cancelled';
    if(a.action==='submitted')Object.assign(connection.transaction.attempt,{state:'pending',hash:a.hash});
    if(a.action==='verify'){
     connection.transaction.attempt.state='confirmed';
     if(connection.transaction.kind==='permission')state.publisherGranted=true;
     else state.criteria={...connection.expected};
    }
    if(a.action==='finish'){
     assert.equal(state.publisherGranted,true);assert.deepEqual(state.criteria,connection.expected);connection.completed=true;
    }
   }
   res.setHeader('Content-Type','application/json');res.end(JSON.stringify({session}));return;
  }
  if(req.url==='/shop-connect')res.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'; connect-src 'self' https:; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'");
  const path=req.url==='/shop-connect'?'web/shop-connect.html':req.url.startsWith('/fonts/')?'templates/croptop/assets/'+req.url.slice(7):req.url.startsWith('/shop-assets/')?'templates/croptop/assets/'+req.url.slice(13):'web'+req.url;
  try{let body=await readFile(root+path);if(path.endsWith('ethers.umd.min.js'))body=Buffer.concat([body,Buffer.from('\n'+mock)]);
   res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':path.endsWith('.css')?'text/css':path.endsWith('.png')?'image/png':path.endsWith('.woff2')?'font/woff2':'text/html');res.end(body);
  }catch{res.statusCode=404;res.end();}
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await chromium.launch({headless:true});
 try{
  const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.route('https://**',r=>{external.push(r.request().url());r.abort();});
  await page.goto(`http://127.0.0.1:${server.address().port}/shop-connect#${site}/8453/${id}/${token}`);
  await page.locator('#review-setup:enabled').waitFor();
  assert.equal(await page.locator('#connect-price').inputValue(),'0.001');
  await page.locator('#connect-price').fill('0.005');
  assert.match(await page.locator('#posting-rules').innerText(),/2 to 50.*7.5%/);
  assert.equal(actions.filter(a=>a.action==='signing').length,0);
  await page.screenshot({path:`/tmp/croptop-connect-${revnet?'revnet':'project'}-desktop.png`,fullPage:true});
  await page.setViewportSize({width:390,height:844});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  assert.ok((await page.locator('.shop-price').boundingBox()).width<=320);
  await page.screenshot({path:`/tmp/croptop-connect-${revnet?'revnet':'project'}-mobile.png`,fullPage:true});
  await page.getByRole('button',{name:'Review setup',exact:true}).click();
  await page.locator('#setup-review:not([hidden])').waitFor();
  assert.match(await page.locator('#step-description').innerText(),revnet?/minimum post price/:/Allow Croptop/);
  await page.getByRole('button',{name:'Approve in wallet',exact:true}).click();
  await page.getByText('Wallet approval cancelled.',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Review saved transaction'}).click();
  await page.locator('#setup-review:not([hidden])').waitFor();
  await page.getByRole('button',{name:'Approve in wallet',exact:true}).click();
  await page.getByText('Waiting for network finality.',{exact:true}).waitFor();
  const nonces=actions.filter(a=>a.action==='signing').map(a=>a.nonce);assert.deepEqual(nonces,['0x5','0x5']);
  await page.reload();await page.getByRole('button',{name:'Check transaction'}).click();
  if(!revnet){
   await page.getByText('Permission confirmed. Review setup to continue with the posting rules.',{exact:true}).waitFor();
   assert.equal(await page.getByRole('button',{name:'Connect shop',exact:true}).isVisible(),false);
   await page.getByRole('button',{name:'Review setup',exact:true}).click();
   await page.locator('#setup-review:not([hidden])').waitFor();
   await page.evaluate(()=>{window.rejectNext=false;});
   await page.getByRole('button',{name:'Approve in wallet',exact:true}).click();
   await page.getByText('Waiting for network finality.',{exact:true}).waitFor();
   await page.getByRole('button',{name:'Check transaction'}).click();
  }
  await page.getByText('Shop connected. Publish your site when you’re ready.',{exact:true}).waitFor();
  assert.equal(actions.filter(a=>a.action==='submitted').length,revnet?1:2);
  assert.deepEqual(errors,[]);assert.deepEqual(external,[]);
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});
