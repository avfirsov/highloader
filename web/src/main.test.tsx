                                                                                            
                   
                                                                         
                                                
                             
   
                            
                                                       
                                                        
import {expect,it,vi} from 'vitest';
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {Timeline} from './Timeline';
import {PolicyEditor} from './Editors';
import type {Policy} from './types';
                                                                                    
                                                          
                                                                            
                
   
it('edits timeline through keyboard with exact controlled values',async()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const host=document.createElement('div');document.body.append(host);const root=createRoot(host);const change=vi.fn();await act(async()=>root.render(<Timeline profile={{mode:'open',retryAccounting:'within',steps:[{durationSeconds:30,level:10}]}} onChange={change}/>));await act(async()=>host.querySelector('circle')!.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true})));expect(change.mock.calls[0][0].steps[0].level).toBe(11);await act(async()=>root.unmount());host.remove();vi.unstubAllGlobals();console.info('Keyboard edits controlled stage');});
it('explicit false remains an override until inheritance is selected',async()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const host=document.createElement('div');const root=createRoot(host);const change=vi.fn();const common:Policy={timeoutMs:1000,maxRetries:2,statuses:[{from:500,to:599}],network:true,timeouts:true,delayMode:'fixed',delayMs:100,maxDelayMs:1000,jitter:false,deadlineMs:5000};await act(async()=>root.render(<PolicyEditor common={common} value={{network:false}} onChange={change}/>));const network=Array.from(host.querySelectorAll('.fields>div')).find(el=>el.textContent?.includes('Повторять сетевые ошибки'))!;const inputs=network.querySelectorAll('input');expect(inputs[0].checked).toBe(false);expect(inputs[1].checked).toBe(false);await act(async()=>inputs[0].click());expect(change.mock.calls[0][0]).not.toHaveProperty('network');await act(async()=>root.unmount());vi.unstubAllGlobals();});
                            
