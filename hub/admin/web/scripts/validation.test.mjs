import test from "node:test";
import assert from "node:assert/strict";
import * as validate from "../src/lib/validation.ts";

const now = "2026-10-07T00:00:00Z";
function fixture() { return [
  {hub:{public_ip:"192.0.2.1",wg_ip:"10.66.1.1",version:"test",uptime_seconds:0},stats:{token_count:0,enabled_token_count:0,active_lease_count:0,egress_online_count:0,rotate_today_count:0},updated_at:now},
  {tokens:[]},{leases:[]},{egress:[]},{events:[]},
]; }
test("empty live inventory and zero metrics are valid", () => { validate.snapshot(...fixture()); });
test("canonical optional nullable fields may be absent, but malformed present values fail", () => {
  const f=fixture();
  f[3].egress=[{id:"synthetic",display_name:"test",region:"JP",type:"android-reverse",management_addr:"",proxy_addr:"",status:"offline"}];
  f[4].events=[{id:1,actor:"test",source_ip:"192.0.2.1",event_type:"test",target:"synthetic",result:"test",occurred_at:now}];
  validate.snapshot(...f);
  for (const [row,key,bad] of [[f[3].egress[0],"rotate_lock_until","invalid"],[f[3].egress[0],"raw_health",[]],[f[4].events[0],"detail",[]],[f[4].events[0],"error_code",5]]) {
    row[key]=null;validate.snapshot(...f);
    row[key]=bad;assert.throws(()=>validate.snapshot(...f));
    delete row[key];
  }
});
test("local rotate state is explicit and unknown is a valid blocking projection", () => {
  const f=fixture();
  f[3].egress=[{id:"synthetic",display_name:"test",region:"JP",type:"android-reverse",management_addr:"",proxy_addr:"",status:"offline"}];
  for(const state of ["idle","cooldown","unknown"]) { f[3].egress[0].rotate_state=state;validate.snapshot(...f); }
  for(const state of ["running",null,0]) { f[3].egress[0].rotate_state=state;assert.throws(()=>validate.snapshot(...f)); }
});
test("forward compatible fields cannot replace required facts", () => { const f=fixture(); f[0].extension=true;validate.snapshot(...f);delete f[0].stats;assert.throws(()=>validate.snapshot(...f)); });
for (const [name,change] of [
  ["null overview",f=>f[0]=null], ["missing hub",f=>f[0].hub={}], ["negative count",f=>f[0].stats.token_count=-1],
  ["NaN count",f=>f[0].stats.token_count=NaN], ["infinite count",f=>f[0].stats.token_count=Infinity], ["string count",f=>f[0].stats.token_count="0"],
  ["missing tokens",f=>f[1]={}], ["object tokens",f=>f[1].tokens={}], ["null token",f=>f[1].tokens=[null]],
  ["malformed lease",f=>f[2].leases=[{}]], ["malformed egress",f=>f[3].egress=[{}]], ["malformed event",f=>f[4].events=[{}]],
  ["invalid timestamp",f=>f[0].updated_at="invalid"],
]) test(`snapshot refuses ${name} before assignment`,()=>{const f=fixture();change(f);assert.throws(()=>validate.snapshot(...f));});
test("identity bound secret and exit receipts",()=>{
  assert.equal(validate.tokenSecret({id:"a",token:"synthetic"},"a").token,"synthetic");
  assert.throws(()=>validate.tokenSecret({id:"b",token:"synthetic"},"a"));
  validate.exitIP({egress_id:"a",exit_ip:"192.0.2.1",checked_at:now},"a");
  assert.throws(()=>validate.exitIP({egress_id:"b",exit_ip:"192.0.2.1",checked_at:now},"a"));
});
test("rotate receipt validates action and target",()=>{
  validate.rotate({egress_id:"a",down_seconds:8,status:"triggered"},"a",8);
  validate.rotate({egress_id:"a",down_seconds:8,status:"busy",retry_after_seconds:0},"a",8);
  for(const x of [{egress_id:"a",down_seconds:8,status:"unknown"},{egress_id:"b",down_seconds:8,status:"triggered"},{egress_id:"a",down_seconds:1,status:"triggered"}]) assert.throws(()=>validate.rotate(x,"a",8));
});
test("session expiry and missing authorization facts refused",()=>{
  validate.session({username:"test",csrf_token:"test",expires_at:new Date(Date.now()+60000).toISOString()});
  for(const x of [null,{}, {username:"test",csrf_token:"",expires_at:now},{username:"test",csrf_token:"test",expires_at:"2000-01-01T00:00:00Z"}]) assert.throws(()=>validate.session(x));
});
