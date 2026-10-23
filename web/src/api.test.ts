                                                                                              
                   
                                                                                                 
                                                                              
                                                                                    
                                          
   
                            
                                                                         
                                                                                      
import {afterEach,expect,it,vi} from 'vitest';
import {Client} from './api';
afterEach(()=>vi.unstubAllGlobals());
                                                                                                  
                                                                              
                                                                        
                
   
it('uses the global fetch receiver for default session and mutation transport',async()=>{
 const transport=vi.fn(function(this:unknown,input:RequestInfo|URL,init?:RequestInit){
  if(this!==globalThis)throw new TypeError('Illegal invocation: fetch requires its global receiver');
  return Promise.resolve(String(input).endsWith('/session')
   ?new Response(JSON.stringify({epoch:'browser-session',csrfToken:'test-token'}))
   :new Response(null,{status:204}));
 });
 vi.stubGlobal('fetch',transport);
 const api=new Client(),abort=new AbortController();
 await api.connect(abort.signal);
 expect(api.epoch).toBe('browser-session');
 const body={expectedRevision:'9007199254740993',settings:{}};
 expect(await api.request('/draft','PUT',body,abort.signal)).toBeUndefined();
 expect(transport).toHaveBeenNthCalledWith(1,'/api/v1/session',expect.objectContaining({method:'GET',signal:abort.signal,credentials:'same-origin',headers:{},body:undefined}));
 expect(transport).toHaveBeenNthCalledWith(2,'/api/v1/draft',expect.objectContaining({method:'PUT',signal:abort.signal,credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':'test-token'},body:JSON.stringify(body)}));
 console.info('Default fetch retains global receiver, session, CSRF, abort signal and exact revision');
});
                                  
