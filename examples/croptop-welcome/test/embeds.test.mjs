import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
import {test} from 'node:test';
const context = vm.createContext({URL});
vm.runInContext(readFileSync(new URL('../embeds.js', import.meta.url), 'utf8'), context);
const validate = context.croptopEmbedURL;
const site = 'https://juicebox.money/';
const parent = 'https://crop.top';
const good = {croptop:{version:1,embed:'/',origins:[parent,'https://croptop.eth.sucks']}};
test('accepts an explicitly approved site and same-origin embed path', () => {
  assert.equal(validate(good,site,parent),site);
  assert.equal(validate(good,site,'https://croptop.eth.sucks'),site);
});
test('missing, unsupported and malformed opt-ins do not embed', () => {
  for(const manifest of [null,{}, {croptop:true}, {croptop:{...good.croptop,version:2}}, {croptop:{...good.croptop,origins:parent}}]) {
    assert.equal(validate(manifest,site,parent),null);
  }
});
test('origins are exact; wildcards, opaque origins and lookalikes do not grant access', () => {
  for(const origin of ['null','https://evil.example','https://crop.top.evil.example','http://crop.top']) assert.equal(validate(good,site,origin),null);
  assert.equal(validate({croptop:{...good.croptop,origins:['*']}},site,parent),null);
});
test('embedded URLs cannot change origin, run scripts, carry credentials or share the parent origin', () => {
  for(const embed of ['https://evil.example/','//evil.example/','javascript:alert(1)','https://user:pass@juicebox.money/']) {
    assert.equal(validate({croptop:{...good.croptop,embed}},site,parent),null);
  }
  assert.equal(validate(good,parent,parent),null);
  assert.equal(validate(good,'http://juicebox.money/',parent),null);
});
