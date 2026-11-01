import {afterEach,expect,it,vi} from 'vitest';
import {refreshCredentialSource} from './fullRefresh';
afterEach(() => vi.unstubAllGlobals());
it('uses only the common76 host-issued source/job/progress for explicit credential refresh',async () => {
 const source={kind:'credential_quota',credential:{auth_index:'a',scope:{instance_id:'issued'},targets:[{resource_id:'issued-target'}],sourceVersion:'v1',version:7}};
 const calls:Array<[string,RequestInit|undefined]>=[];
 vi.stubGlobal('fetch',vi.fn(async(url:string,init?:RequestInit) => {
  calls.push([url,init]);
  return Response.json(init?.method==='POST' ? {id:'common-job',state:'complete',pending:0,failed:0,items:[]} : {sources:[{id:'source',request:source,targets:source.credential.targets,queryCapable:true}]} );
 }));
 await refreshCredentialSource('http://fixture','test','a');
 expect(calls.map(c=>c[0])).toEqual(['http://fixture/usage-service/full-refresh','http://fixture/usage-service/full-refresh']);
 expect(JSON.parse(String(calls[1][1]?.body))).toEqual({purpose:'manual',source});
 expect(calls.every(c=>c[1]?.headers && c[1]?.redirect==='error' && c[1]?.cache==='no-store')).toBe(true);
});
