                                                                                               
                   
                                                                                                             
                                                                                               
                                                                               
                                                            
                                                                  
   
                            
                                                                      
                                                                                  
import {afterEach,expect,it,vi} from 'vitest';
import {Client} from './api';
afterEach(()=>{vi.useRealTimers();vi.restoreAllMocks();vi.unstubAllGlobals();});

                                                                                                 
                                                                    
                                                                                         
                
   
it.each([
 ['{"message":"revision conflict","error":"fallback"}',409,'revision conflict'],
 ['{"error":"not found"}',404,'not found'],
 ['{}',503,'Ошибка 503'],
 ['upstream unavailable',502,'Ошибка 502'],
])('surfaces API response %s without implicit retry',async(body,status,message)=>{
 const transport=vi.fn().mockResolvedValue(new Response(body,{status}));
 await expect(new Client(transport).request('/runs','POST',{})).rejects.toThrow(message);
 expect(transport).toHaveBeenCalledTimes(1);
});
it('propagates network, abort and malformed-success failures without retry',async()=>{
 for(const failure of [new TypeError('offline'),new DOMException('canceled','AbortError')]){
  const transport=vi.fn().mockRejectedValue(failure);
  await expect(new Client(transport).request('/active')).rejects.toBe(failure);
  expect(transport).toHaveBeenCalledTimes(1);
 }
 await expect(new Client(vi.fn().mockResolvedValue(new Response('bad json'))).request('/draft')).rejects.toThrow();
 console.info('HTTP, network, abort and invalid JSON remain observable without retry');
});
                                  

                                                                                                 
                                                                                              
                                                                      
                
   
it('keeps the latest session when an older bootstrap finishes later',async()=>{
 let finish!:(r:Response)=>void;
 const transport=vi.fn().mockImplementationOnce(()=>new Promise<Response>(resolve=>{finish=resolve;}))
  .mockResolvedValueOnce(new Response(JSON.stringify({epoch:'new',csrfToken:'new-token'})))
  .mockResolvedValueOnce(new Response(null,{status:204}));
 const client=new Client(transport),old=client.connect();await client.connect();
 finish(new Response(JSON.stringify({epoch:'old',csrfToken:'old-token'})));await old;
 expect(client.epoch).toBe('new');await client.request('/history','DELETE');
 expect(transport.mock.calls[2][1].headers).toEqual({'X-CSRF-Token':'new-token'});
 expect(transport.mock.calls[2][1].body).toBeUndefined();
});
                                 

                                                                                                  
                                                            
                                                                                   
                
   
it('downloads the returned HTML blob and revokes only after the download begins',async()=>{
 vi.useFakeTimers();const create=vi.fn(()=> 'blob:local-report'),revoke=vi.fn();
 vi.stubGlobal('URL',Object.assign(class extends URL {},{createObjectURL:create,revokeObjectURL:revoke}));
 const click=vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(function(this:HTMLAnchorElement){
  expect(this.download).toBe('highloader-report.html');expect(this.href).toBe('blob:local-report');
 });
 const transport=vi.fn().mockResolvedValue(new Response('<html>evidence</html>',{headers:{'Content-Type':'text/html'}}));
 await new Client(transport).download({runId:'run',includeAuthorization:false});
 expect(click).toHaveBeenCalledTimes(1);expect(create).toHaveBeenCalledTimes(1);expect(revoke).not.toHaveBeenCalled();
 expect(JSON.parse(transport.mock.calls[0][1].body)).toEqual({runId:'run',includeAuthorization:false});
 await vi.advanceTimersByTimeAsync(999);expect(revoke).not.toHaveBeenCalled();
 await vi.advanceTimersByTimeAsync(1);expect(revoke).toHaveBeenCalledWith('blob:local-report');
 console.info('Offline HTML download retains export policy and releases its object URL');
});
                                   
