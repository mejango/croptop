// Run with: PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs node --test web/shop.test.mjs
// All wallet and node responses are mocked. No blockchain requests are made.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('../', import.meta.url));
const ethers = require(root + 'templates/croptop/assets/ethers.umd.min.js');
const wallet = '0x1111111111111111111111111111111111111111';
const siteID = '11111111-1111-1111-1111-111111111111';
const sessionID = 'a'.repeat(64), token = 'b'.repeat(64);
const mockWallet = `
window.shopSends = []; window.rejectChain = 10;
window.ethereum = { chain: 8453, networkVersion:'8453', async request({method,params}) {
 if (method==='eth_requestAccounts') return ['${wallet}'];
 if (method==='wallet_switchEthereumChain') {this.chain=Number(params[0].chainId);this.networkVersion=String(this.chain);return null;}
 throw new Error('Unexpected wallet request '+method);
}};
class ShopMockProvider {
 constructor(url) {this.chain=url?.includes?.('optimism')?10:8453;}
 async getNetwork() {return {chainId:BigInt(this.chain)};}
 async getCode() {return '0x1234';}
 async call(tx) {return tx.data==='0xdce0b4e4'?'0x'+'0'.repeat(64):'0x'+'0'.repeat(63)+'7'+'0'.repeat(24)+'2'.repeat(40);}
 async estimateGas() {return 100000n;}
 async getFeeData() {return {maxFeePerGas:1000000000n};}
 async resolveName(name) {return name;}
}
ethers.JsonRpcProvider=ShopMockProvider;
ethers.BrowserProvider=class extends ShopMockProvider {
 constructor(){super();this.chain=window.ethereum.chain;}
 async send(method) {if(method==='eth_getTransactionCount')return '0x5';throw new Error(method);}
 async getSigner(){return {getAddress:async()=>'${wallet}',sendTransaction:async(tx)=>{
  if(window.rejectChain===this.chain){window.rejectChain=null;throw Object.assign(new Error('Signing cancelled'),{code:'ACTION_REJECTED'});}
  window.shopSends.push({chain:this.chain,nonce:tx.nonce});return {hash:'0x'+this.chain.toString(16).padStart(64,'0')};
 }};}
};`;

test('shop UI reviews, keeps partial results, resumes after reload and saves both networks', async () => {
  let session = { id:sessionID, siteId:siteID, siteName:'My site', salt:'0x'+'c'.repeat(64), startsAt:Math.floor(Date.now()/1000)+360, chains:[] };
  const actions = [], external = [];
  const server = http.createServer(async (req,res) => {
    if(req.url.startsWith('/v0/')) {
      assert.equal(req.headers['x-shop-token'],token);
      if(req.method==='POST') {
        let body='';for await(const part of req) body+=part;const input=JSON.parse(body);actions.push(input);
        if(input.action==='plan') {session.config=input.config;session.chains=input.chains;}
        else {
          const chain=session.chains.find(c=>c.id===input.chainId), last=chain?.attempts.at(-1);
          if(input.action==='signing') {
            if(last?.state==='cancelled'||last?.state==='signing'){assert.equal(input.nonce,last.nonce);last.state='signing';}
            else chain.attempts.push({state:'signing',from:input.from,nonce:input.nonce});
          }
          if(input.action==='cancelled') last.state='cancelled';
          if(input.action==='submitted') Object.assign(last,{state:'pending',hash:input.hash});
          if(input.action==='verify') {chain.address='0x'+(chain.id===8453?'2':'3').repeat(40);chain.applied=true;last.state='confirmed';}
        }
      }
      res.setHeader('Content-Type','application/json');res.end(JSON.stringify({session}));return;
    }
    if(req.url==='/shop') res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; connect-src 'self' https:; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'");
    const path=req.url==='/shop'?'web/shop.html':req.url.startsWith('/fonts/')?'templates/croptop/assets/'+req.url.slice(7):req.url.startsWith('/shop-assets/')?'templates/croptop/assets/'+req.url.slice(13):'web'+req.url;
    try {
      let body=await readFile(root+path);
      if(path.endsWith('ethers.umd.min.js'))body=Buffer.concat([body,Buffer.from('\n'+mockWallet)]);
      res.setHeader('Content-Type',path.endsWith('.woff2')?'font/woff2':path.endsWith('.js')?'text/javascript':path.endsWith('.css')?'text/css':'text/html');res.end(body);
    } catch {res.statusCode=404;res.end();}
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const browser=await chromium.launch({headless:true});
  try {
    const page=await browser.newPage(); const errors=[];page.on('pageerror',e=>errors.push(e.message));
    await page.route('https://**',route=>{external.push(route.request().url());route.abort();});
    await page.goto(`http://127.0.0.1:${server.address().port}/shop#${siteID}/${sessionID}/${token}`);
    await page.locator('#details:not([hidden])').waitFor();
    assert.equal(await page.locator('[name=name]').inputValue(),'My site');
    assert.equal(await page.locator('#get-quote').isEnabled(),false);
    await page.locator('[name=symbol]').fill('MYSITE');
    await page.getByRole('button',{name:'connect wallet',exact:true}).click();
    assert.equal(await page.locator('[name=owner]').inputValue(),wallet);
    assert.equal(actions.filter(a=>a.action==='plan'||a.action==='signing').length,0,'connecting must not plan or sign');
    await page.locator('[name=chain][value="10"]').check();
    await page.getByRole('button',{name:'get deploy quote'}).click();
    await page.locator('#review:not([hidden])').waitFor();
    assert.match(await page.locator('#costs').innerText(),/Base:.*creation fee/);
    assert.equal(actions.filter(a=>a.action==='signing').length,0,'review sent a transaction');
    // The actual shared builder/ABI produced per-chain deployment data.
    assert.equal(session.chains.length,2);
    assert.ok(session.chains.every(c=>c.data.includes(session.salt.slice(2))));
    await page.getByRole('button',{name:'Approve in wallet',exact:true}).click();
    await page.getByText('Signing cancelled',{exact:true}).first().waitFor();
    // Chain order is Optimism then Base: first signing was rejected.
    assert.equal(session.chains[0].attempts.at(-1).state,'cancelled');
    await page.getByRole('button',{name:'Review remaining networks'}).click();
    await page.locator('#review:not([hidden])').waitFor();
    await page.getByRole('button',{name:'Approve in wallet',exact:true}).click();
    await page.getByText('Transactions submitted.',{exact:false}).waitFor();
    assert.equal(session.chains.filter(c=>c.attempts.at(-1).state==='pending').length,2);
    await page.locator('.chain').first().getByRole('button',{name:'Check transaction'}).click();
    await page.getByText('1 of 2 networks saved.',{exact:false}).waitFor();
    await page.reload();
    await page.getByText('1 of 2 networks saved.',{exact:false}).waitFor();
    assert.equal(await page.getByRole('button',{name:'Review remaining networks'}).count(),0);
    await page.locator('.chain').last().getByRole('button',{name:'Check transaction'}).click();
    await page.getByText('2 of 2 networks saved.',{exact:false}).waitFor();
    assert.notEqual(session.chains[0].address,session.chains[1].address);
    assert.equal(actions.filter(a=>a.action==='submitted').length,2);
    assert.deepEqual(external,[]);assert.deepEqual(errors,[]);
    await page.screenshot({path:'/tmp/croptop-shop-ui.png',fullPage:true});
  } finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
});
