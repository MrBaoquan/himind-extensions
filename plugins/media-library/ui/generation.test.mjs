import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
const window={};runInNewContext(readFileSync(new URL('./generation.js',import.meta.url),'utf8'),{window});
const {draftStore,referenceLimit,duplicateRisk}=window.MediaLibraryGeneration;
test('visual descriptions and spoken scripts keep separate drafts and immutable saved references',()=>{
 const store=draftStore({});const refs=[{id:'image-a'}];store.set('image.generate',{prompt:'一只咖啡杯',references:refs});refs.push({id:'other'});
 assert.equal(store.get('audio.speech').prompt,'');store.set('audio.speech',{prompt:'欢迎收听'});
 assert.equal(store.get('image.generate').prompt,'一只咖啡杯');assert.equal(store.get('image.generate').references.length,1);assert.equal(store.get('audio.speech').prompt,'欢迎收听');
});
test('a configured single-image adapter never promises four references',()=>{
 const catalog={routes:[{capability:'image.edit',selected:{adapter:'single'}}],adapters:[{id:'single',limits:{input_images:1}}]};
 assert.equal(referenceLimit(catalog,'image.edit'),1);assert.equal(referenceLimit(null,'video.generate'),1);assert.equal(referenceLimit(catalog,'image.generate'),0);
});
test('new paid request warning applies to active and uncertain matching work, not completed work',()=>{
 const draft={capability:'image.generate',prompt:'咖啡杯'},base={...draft};
 assert.equal(duplicateRisk([{...base,job:{status:'running'}}],draft),true);
 assert.equal(duplicateRisk([{...base,submission_state:'unknown'}],draft),true);
 assert.equal(duplicateRisk([{...base,job:{status:'succeeded'}}],draft),false);
 assert.equal(duplicateRisk([{...base,submission_state:'prepared'}],draft),false);
 assert.equal(duplicateRisk([{...base,job:{status:'running'},capability:'audio.speech'}],draft),false);
});
