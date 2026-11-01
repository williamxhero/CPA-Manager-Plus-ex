import {act,create,type ReactTestRenderer} from 'react-test-renderer';
import {afterEach,expect,it,vi} from 'vitest';
import {CommonQuotaInventoryPanel} from './CommonQuotaInventoryPanel';
afterEach(()=>vi.unstubAllGlobals());
it('separates native selected-key quota from wallet money and keeps unavailable unknown, not zero',async()=>{
 vi.stubGlobal('fetch',vi.fn(async()=>Response.json({sources:[
 {id:'key',request:{kind:'official_balance'},targets:[],state:'ready',stale:false,lastSuccess:{semantic:'key_remaining',nativeQuota:{unit:'newapi_quota',state:'finite',remaining:'123'}}},
 {id:'wallet',request:{kind:'official_balance'},targets:[],state:'ready',stale:true,lastSuccess:{semantic:'account_wallet',currency:'USD',amountDecimal:'-2.50'}},
 {id:'unknown',request:{kind:'unavailable'},targets:[],state:'unsupported',reason:'no_protocol',stale:false}
 ]})));
 let r!:ReactTestRenderer;
 await act(async()=>{r=create(<CommonQuotaInventoryPanel base="http://fixture" managementKey="test"/>);});
 const text=JSON.stringify(r.toJSON());expect(text).toContain('123');expect(text).toContain('key_remaining');expect(text).toContain('-2.50');expect(text).toContain('account_wallet');expect(text).toContain('unknown');expect(text).not.toContain('USD 0');
 await act(async()=>r.unmount());
});
