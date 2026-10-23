                                                                                                 
                   
                                                                                            
                                                                                                    
                                                                                
                                                                                    
                                                                                             
                                                                                                    
                                     
   
                            
                                                                                      
                                                                                         
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {JSDOM} from 'jsdom';

                                                                                             
   
                                                                                                
                                                        
                
   
function checkReportRuntime(artifactPath) {
  // bytes → empty browser → malformed-report browser → visible fallback
  const source = readFileSync(artifactPath, 'utf8');
  console.info(`bytes=${Buffer.byteLength(source)}`);
  for (const hasRoot of [false, true]) {
    const dom = new JSDOM(hasRoot ? '<div id="root"></div>' : '', {runScripts:'outside-only', url:'file:///offline-report.html'});
    try {
      assert.equal(typeof dom.window.process, 'undefined');
      assert.equal(typeof dom.window.require, 'undefined');
      // execute actual bundled dependencies; string matching alone cannot detect initialization failures.
      dom.window.eval(source);
      assert.equal(typeof dom.window.process, 'undefined', 'bundle must not install a process shim');
      if (hasRoot) assert.equal(dom.window.document.getElementById('root').textContent, 'Не удалось прочитать встроенный отчёт.');
      console.info(`root=${hasRoot} initialized without Node globals`);
    } finally {
      dom.window.close();
    }
  }
}
                                    

checkReportRuntime(process.argv[2] ?? fileURLToPath(new URL('../report-dist/report.js', import.meta.url)));
