import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {webcrypto} from 'node:crypto';

const context={window:{},Intl,URL,console};vm.createContext(context);
vm.runInContext(fs.readFileSync(new URL('./comments.js',import.meta.url),'utf8'),context);
test('public comment text is escaped and unknown likes stay unknown',()=>{
 const html=context.window.MediaLibraryComments.card({author:'<昵称>',text:'<script>alert(1)</script>',avatar:'https://bad.test/x'},false);
 assert.ok(html.includes('&lt;script&gt;'));assert.ok(!html.includes('<script>'));assert.ok(!html.includes('bad.test'));assert.ok(!html.includes('点赞 0'));
});
test('zero likes, author marker and child replies remain explicit',()=>{
 const html=context.window.MediaLibraryComments.card({author:'作者',text:'正文',like_count:0,is_author:true,replies:[{author:'回复',text:'回复正文',like_count:2}]},false);
 assert.ok(html.includes('点赞 0'));assert.ok(html.includes('comment-badge'));assert.ok(html.includes('展开 1 条已读取回复'));assert.ok(html.includes('回复正文'));
});

function harness() {
 const nodes=new Map(),calls=[];
 const element=id=>{
  if(!nodes.has(id)) nodes.set(id,{value:id==='comments-sort'?'official':'',textContent:'',innerHTML:'',disabled:false,hidden:false,events:{},classList:{toggle(){}},setAttribute(){},addEventListener(event,fn){this.events[event]=fn;}});
  return nodes.get(id);
 };
 const env={window:{},document:{getElementById:element},crypto:webcrypto,Intl,URL,console};vm.createContext(env);
 vm.runInContext(fs.readFileSync(new URL('./comments.js',import.meta.url),'utf8'),env);
 const invoke=(id,input)=>new Promise((resolve,reject)=>calls.push({id,input,resolve,reject}));
 return {view:env.window.MediaLibraryComments.create({invoke}),calls,element};
}
const tick=()=>new Promise(resolve=>setImmediate(resolve));
test('comments are lazy, saved snapshots load offline, and switching rejects stale responses',async()=>{
 const h=harness();h.view.begin({source_url:'https://www.douyin.com/video/7624170206072269129'});assert.equal(h.calls.length,0);
 const first=h.view.activate();assert.equal(h.calls[0].id,'media-library.comments.snapshot.get');
 h.view.begin({source_url:'https://www.douyin.com/video/7702136163577924874'});
 h.calls[0].resolve({snapshot:{items:[{text:'旧素材',author:'旧作者'}]}});await first;assert.equal(h.element('comments-list').innerHTML,'');
 const second=h.view.activate();h.calls[1].resolve({snapshot:{items:[{text:'本地快照',author:'公开作者',like_count:0}],observed_at:'2026-11-15T00:00:00Z'}});await second;
 assert.ok(h.element('comments-list').innerHTML.includes('本地快照'));assert.equal(h.calls.length,2);assert.equal(h.element('comments-save').disabled,true);
});
test('cancel invalidates late comment pages and failures expose retry',async()=>{
 const h=harness();h.view.begin({source_url:'https://www.douyin.com/video/7624170206072269129'});
 const action=h.view.activate();h.calls[0].resolve({snapshot:null});await tick();assert.equal(h.calls[1].id,'media-library.comments.list');
 h.element('comments-cancel').events.click();await tick();assert.equal(h.calls[2].id,'media-library.discovery.cancel');
 h.calls[1].resolve({items:[{author:'晚到',text:'不应显示'}]});h.calls[2].resolve({});await action;
 assert.equal(h.element('comments-list').innerHTML,'');assert.ok(h.element('comments-status').textContent.includes('取消'));
 h.element('comments-refresh').events.click();await tick();h.calls[3].reject(new Error('官网暂不可用'));await tick();
 assert.ok(h.element('comments-status').textContent.includes('暂不可用'));assert.equal(h.element('comments-refresh').disabled,false);assert.equal(h.element('comments-save').disabled,true);
});
