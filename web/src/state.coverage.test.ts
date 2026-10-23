                                                                                              
                   
                                                                                                
                                                                                    
                                                                                                    
                                                          
                                           
   
                            
                                                                       
                                                                               
import {expect,it} from 'vitest';
import {terminal,decodeRaw,moveStep} from './state';
import {maskURL,secretHeader} from './privacy';
import {LiveOwner} from './ownership';
import type {View} from './types';
                                                                                             
                                                                               
                                                                                           
                
   
it('requires durable released execution and writer across every transient state',()=>{
 const released={runId:'r',revision:'1',state:'completed',workersReleased:true,writerReleased:true,durable:true,persistenceError:''} as View;
 expect(terminal(null)).toBe(true);expect(terminal(released)).toBe(true);
 for(const patch of [{workersReleased:false},{writerReleased:false},{durable:false},{persistenceError:'disk full'},...['running','stopping','draining','starting','finalizing'].map(state=>({state}))])expect(terminal({...released,...patch})).toBe(false);
 expect(decodeRaw('***')).toBe('Исходные данные недоступны');
 const steps=[{level:1,durationSeconds:2},{level:3,durationSeconds:4}];expect(moveStep(steps,0,'level',2.6)).toEqual([{level:3,durationSeconds:2},steps[1]]);
 console.info('Transient execution and failed persistence never unlock editing');
});
it('classifies configured credentials and masks malformed URLs without changing clear values',()=>{
 for(const name of ['Authorization','COOKIE','X-Api-Key','Access_Token','Secret'])expect(secretHeader(name)).toBe(true);
 expect(secretHeader('Custom-Key','custom-key')).toBe(true);expect(secretHeader('Accept')).toBe(false);expect(secretHeader('Accept','Other')).toBe(false);
 expect(maskURL('http://localhost/path')).toBe('http://localhost/path');
 expect(maskURL('not-a-url?secret=value')).toBe('not-a-url?•••');expect(maskURL('//user:pass@host/path')).toBe('//•••@host/path');expect(maskURL('plain')).toBe('plain');
 expect(maskURL('http://u:p@localhost/path?a=one&a=two')).not.toMatch(/one|two|u:p/);
});
it('accepts only owned run changes and null views when no run exists',()=>{
 const owner=new LiveOwner();owner.reconnect('session',null);const empty=owner.ticket();expect(owner.accept(empty,null)).toBe(true);
 const first={runId:'a',revision:'1'} as View;expect(owner.accept(empty,first)).toBe(false);expect(owner.accept(empty,first,true)).toBe(true);
 expect(owner.accept(owner.ticket(),null)).toBe(false);expect(owner.accept({...owner.ticket(),session:'old'},first)).toBe(false);
 expect(owner.accept({...owner.ticket(),runId:'wrong'},{...first,runId:'b'},true)).toBe(false);
 expect(owner.accept(owner.ticket(),{...first,revision:'2'})).toBe(true);expect(owner.accept(owner.ticket(),first)).toBe(false);
});
                            
