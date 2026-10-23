                                                                                        
                   
                                                                                
                                              
                                 
   
                            
                                                 
                                                            
import {expect,it,vi} from 'vitest';
import {editable,moveStep,decodeRaw,newer,terminal} from './state';
import {Client} from './api';
import type {Draft,View} from './types';
                                                                                       
                                           
                                                               
                
   
it('omits every backend-owned source field from mutations',()=>{const d={templates:[{body:{n:9007199254740992},raw:'original'}],revision:'9007199254740993',schemaVersion:1,url:'http://localhost',selectedIds:['a']} as unknown as Draft;const payload=editable(d);expect(payload).not.toHaveProperty('templates');expect(payload).not.toHaveProperty('revision');expect(JSON.stringify(payload)).not.toContain('original');console.info('Source data excluded from mutation');});
it('preserves authoritative large-number source text',()=>{const raw='{"n":900719925474099312345}';expect(decodeRaw(btoa(raw))).toBe(raw);});
it('clamps stage values and never mutates input',()=>{const steps=[{level:10,durationSeconds:30}];expect(moveStep(steps,0,'level',-2)[0].level).toBe(0);expect(moveStep(steps,0,'durationSeconds',0)[0].durationSeconds).toBe(1);expect(moveStep(steps,0,'level',NaN)).toBe(steps);expect(steps[0].level).toBe(10);});
it('rejects stale revisions beyond safe integer precision',()=>{const old={runId:'r',revision:'9007199254740993'} as View;expect(newer(old,{...old,revision:'9007199254740992'})).toBe(false);expect(newer(old,{...old,revision:'9007199254740994'})).toBe(true);expect(terminal({...old,state:'completed',workersReleased:false})).toBe(false);});
it('bootstraps CSRF and preserves exact expected revision',async()=>{const transport=vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({epoch:'e',csrfToken:'secret'}))).mockResolvedValueOnce(new Response('{}'));const api=new Client(transport as typeof fetch);await api.connect();await api.request('/draft','PUT',{expectedRevision:'9007199254740993',settings:{}});const call=transport.mock.calls[1];expect(call[0]).toBe('/api/v1/draft');expect(call[1].headers['X-CSRF-Token']).toBe('secret');expect(JSON.parse(call[1].body).expectedRevision).toBe('9007199254740993');console.info('Mutation carries session token and exact revision');});
                            
