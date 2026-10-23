                                                                                            
                   
                                                                                                         
                                                                                        
                                                                                               
                                                    
                     
   
                            
                                                   
                                                                                    
import {afterEach,expect,it,vi} from 'vitest';
import {act} from 'react';
import {createRoot,type Root} from 'react-dom/client';
vi.mock('react-dom/client',async importOriginal=>{const actual=await importOriginal<typeof import('react-dom/client')>();return {...actual,createRoot:vi.fn(actual.createRoot)};});
vi.mock('echarts/core',()=>({use:vi.fn(),init:vi.fn(()=>({setOption:vi.fn(),resize:vi.fn(),dispose:vi.fn()}))}));
let root:Root|undefined,host:HTMLDivElement|undefined;
afterEach(async()=>{if(root)await act(async()=>root!.unmount());root=undefined;host?.remove();host=undefined;vi.unstubAllGlobals();vi.clearAllMocks();});
                                                                                        
                                                                        
                                                                                 
                
   
it('rejects startup without the required application root',async()=>{
 vi.resetModules();await expect(import('./main')).rejects.toThrow('Не найден корневой элемент интерфейса');
});
it('mounts the real application and bootstraps its default local transport',async()=>{
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);host=document.createElement('div');host.id='root';document.body.append(host);
 const fetch=vi.fn(async(input:RequestInfo|URL)=>{
  const path=String(input);let data:unknown;
  if(path.endsWith('/session'))data={epoch:'entry',csrfToken:'entry-token'};
  else if(path.endsWith('/draft'))data=null;
  else if(path.endsWith('/active'))data={runId:null,view:null};
  else if(path.includes('/history?'))data={items:[],nextOffset:null};
  else throw new Error(`Unexpected ${path}`);
  return new Response(JSON.stringify(data));
 });vi.stubGlobal('fetch',fetch);vi.resetModules();
 await act(async()=>{await import('./main');});root=vi.mocked(createRoot).mock.results.at(-1)!.value as Root;
 expect(host.querySelector('h1')?.textContent).toBe('Highloader');expect(host.querySelector('[role="status"]')?.textContent).toBe('Подключено');
 expect(fetch.mock.calls.map(([path])=>String(path))).toEqual(expect.arrayContaining(['/api/v1/session','/api/v1/draft','/api/v1/active','/api/v1/history?offset=0']));
 console.info('Real application mounts and bootstraps local session');
});
                         
