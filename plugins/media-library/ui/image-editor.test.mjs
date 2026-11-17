import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import fs from 'node:fs';
const context={window:{}};vm.runInNewContext(fs.readFileSync(new URL('./image-editor.js',import.meta.url),'utf8'),context);
const api=context.window.MediaLibraryImageEditor;
test('reverse canvas drag produces bounded pixel crop at image edges',()=>{
  const c=api.cropFromDrag({x:1,y:1},{x:0.25,y:0.5},101,55);
  assert.deepEqual(JSON.parse(JSON.stringify(c)),{x:25,y:27,width:76,height:28});
});
test('crop rotates before preserving aspect ratio for single dimension',()=>{
  assert.deepEqual(Array.from(api.dimensions({width:1920,height:1080},{crop:{width:800,height:400},rotate:90,width:200,height:0})),[200,400]);
});
test('a resolved adapter alone cannot bypass unavailable Agent execution',()=>{
  const c={execution_ready:false,execution_message:'更新后重试',catalog:{routes:[{capability:'image.edit',state:'resolved',selected:{adapter:'openai-images-edit'}}]}};
  assert.equal(api.available(c,'local'),'更新后重试');
  c.execution_ready=true;assert.equal(api.available(c,'local'),'');
});
test('global editing remains available when an adapter cannot handle a mask',()=>{
  const c={execution_ready:true,catalog:{routes:[{capability:'image.edit',state:'resolved',selected:{adapter:'ark-images'}}]}};
  assert.equal(api.available(c,'describe'),'');assert.match(api.available(c,'local'),/不支持局部蒙版/);
});
test('invalid image dimensions are explained before crossing the gateway schema',()=>{
  const v={width:1920,height:1080};
  assert.match(api.validate(v,{width:-1,height:0,quality:90}),/输出宽高/);
  assert.match(api.validate(v,{width:0,height:0,quality:90,crop:{x:1900,y:0,width:100,height:100}}),/裁剪区域/);
  assert.equal(api.validate(v,{width:640,height:0,quality:90}),'');
});
