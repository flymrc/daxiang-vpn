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

const permanentBlocks = ["campaign_not_configured","e2e_evidence_unavailable","approved_release_unavailable","revocation_evidence_unavailable","session_cleanup_unavailable","recovery_evidence_unavailable","quiet_window_unavailable","observer_continuity_unavailable","dataplane_startup_unverified"];
const memberBlocks = ["approved_release_unavailable","e2e_evidence_unavailable","revocation_evidence_unavailable","session_cleanup_unavailable","recovery_evidence_unavailable"];
function migrationFixture() {
  return {
    contract_version:2,inventory_registered:false,registry_id:"",approved_inventory_sha256:"",baseline_member_count:0,member_count:0,extra_member_count:0,
    t0:null,ready:false,mode:"observation_only",campaign_configured:false,observation_write_healthy:true,
    valid_token_count:0,observed_token_count:0,unobserved_token_count:0,secure_bootstrap_token_count:0,legacy_token_count:0,unknown_token_count:0,compat_ingress_token_count:0,
    observer_started_at:now,last_observation_at:null,generated_at:now,clients:[],blockers:[...permanentBlocks,"inventory_not_registered"],
    observer:{current_run_id:"c".repeat(32),healthy:true,gap_count:0,last_success_at:null,runs:[{run_id:"c".repeat(32),started_at:now,ended_at:null,state:"open",reason:"none",last_success_at:null}]},
  };
}
function migrationMember() {
  return {token_id:"a".repeat(12),membership:"baseline",source_state:"enabled",owner_ref:"b".repeat(32),shared:false,installation_refs:["d".repeat(32)],lineage_state:"declared_unverified",
    history:{secure_bootstrap_count:2,legacy_count:1,unknown_count:0,compat_ingress_count:1,denied_count:1,error_count:0,first_seen_at:now,last_seen_at:now},
    observed:true,client_product:"zhvpn-cli",client_version:"1.2.3",protocol_version:2,ingress:"trusted_proxy",key_mode:"client_generated",private_key_returned:false,migration_class:"secure_bootstrap",last_seen_at:now,
    blockers:[...memberBlocks,"lineage_unverified","historical_legacy","historical_compat","historical_denied"],
  };
}
function registeredMigration() {
  const f=migrationFixture();const row=migrationMember();
  Object.assign(f,{inventory_registered:true,registry_id:"e".repeat(32),approved_inventory_sha256:"f".repeat(64),baseline_member_count:1,member_count:1,valid_token_count:1,observed_token_count:1,secure_bootstrap_token_count:1,compat_ingress_token_count:1,clients:[row],last_observation_at:now});
  f.blockers=[...new Set([...permanentBlocks,...row.blockers])];return f;
}
test("migration empty observation is NO-GO with no campaign or fake denominator",()=>{
  const f=migrationFixture();assert.equal(validate.migration(f).member_count,0);assert.equal(f.t0,null);assert.equal(f.ready,false);
});
test("the exact old v1 response cannot be consumed as a v2 readiness snapshot",()=>{
  const old=migrationFixture();
  for(const key of ["contract_version","inventory_registered","registry_id","approved_inventory_sha256","baseline_member_count","member_count","extra_member_count","t0","observer"]) delete old[key];
  assert.throws(()=>validate.migration(old),{message:"admin_response_invalid"});
});
test("migration secure latest does not erase negative history or declared lineage blockers",()=>{
  const f=registeredMigration();assert.equal(validate.migration(f).clients[0].history.legacy_count,1);
  for(const code of ["lineage_unverified","historical_legacy","historical_compat","historical_denied"]){
    const changed=structuredClone(f);changed.clients[0].blockers=changed.clients[0].blockers.filter(x=>x!==code);
    assert.throws(()=>validate.migration(changed),undefined,code);
  }
});
test("migration keeps baseline denominator for disabled expired and missing source",()=>{
  for(const source of ["disabled","expired","missing","invalid"]){
    const f=registeredMigration();f.clients[0].source_state=source;f.valid_token_count=0;
    f.clients[0].blockers.push(`source_${source}`,"disposition_required");f.blockers=[...new Set([...f.blockers,...f.clients[0].blockers])];
    const result=validate.migration(f);assert.equal(result.baseline_member_count,1);assert.equal(result.member_count,1);assert.equal(result.valid_token_count,0);
  }
});
test("migration extra and shared declarations remain separately blocked",()=>{
  const f=registeredMigration();const extra=structuredClone(f.clients[0]);extra.token_id="1".repeat(12);extra.membership="extra";extra.shared=true;extra.lineage_state="shared_unverified";
  extra.installation_refs.push("2".repeat(32));extra.blockers=extra.blockers.filter(x=>x!=="lineage_unverified");extra.blockers.push("extra_unregistered","shared_lineage_unverified");
  f.clients.push(extra);f.member_count=2;f.extra_member_count=1;f.valid_token_count=2;f.observed_token_count=2;f.secure_bootstrap_token_count=2;f.compat_ingress_token_count=2;
  f.blockers=[...new Set([...f.blockers,...extra.blockers])];assert.equal(validate.migration(f).baseline_member_count,1);
  f.clients[1].blockers=f.clients[1].blockers.filter(x=>x!=="shared_lineage_unverified");assert.throws(()=>validate.migration(f));
});
test("migration durable failed runs cannot become a healthy zero-gap report",()=>{
  const f=migrationFixture();f.observer.runs.push({run_id:"3".repeat(32),started_at:now,ended_at:now,state:"failed",reason:"write_failed",last_success_at:null});
  f.observer.gap_count=1;f.observer.healthy=false;f.observation_write_healthy=false;f.blockers.push("observer_gap","observation_write_failed");
  validate.migration(f);for(const change of [x=>x.observer.gap_count=0,x=>x.observer.healthy=true,x=>x.blockers=x.blockers.filter(b=>b!=="observer_gap")]){
    const bad=structuredClone(f);change(bad);assert.throws(()=>validate.migration(bad));
  }
});
for(const [name,change] of [
  ["old contract",f=>f.contract_version=1],["claim ready",f=>f.ready=true],["started campaign",f=>f.campaign_configured=true],["assigned T0",f=>f.t0=now],
  ["missing observer",f=>delete f.observer],["missing required permanent blocker",f=>f.blockers=f.blockers.filter(x=>x!=="approved_release_unavailable")],
  ["unknown blocker",f=>f.blockers.push("synthetic_secret")],["infinite denominator",f=>f.member_count=Infinity],["negative count",f=>f.baseline_member_count=-1],
  ["count drift",f=>f.observed_token_count=1],["duplicate observer run",f=>f.observer.runs.push(structuredClone(f.observer.runs[0]))],
  ["unknown current run",f=>f.observer.current_run_id="4".repeat(32)],["invalid generated date",f=>f.generated_at="bad"],
  ["open run already ended",f=>f.observer.runs[0].ended_at=now],["open run has failure reason",f=>f.observer.runs[0].reason="write_failed"],
  ["run end predates start",f=>f.observer.runs[0].ended_at="2000-01-01T00:00:00Z"],
])test(`migration rejects ${name} before snapshot commit`,()=>{const f=migrationFixture();change(f);assert.throws(()=>validate.migration(f));});
test("migration malformed members and finite resource limits fail closed",()=>{
  for(const change of [f=>f.clients.push(structuredClone(f.clients[0])),f=>f.clients[0].history.legacy_count=NaN,f=>f.clients[0].installation_refs.push("not-an-opaque-id"),
    f=>f.clients[0].owner_ref="SYNTHETIC_SECRET",f=>f.clients[0].lineage_state="confirmed",f=>f.clients[0].lineage_state="unknown",f=>f.clients[0].blockers=[],f=>f.clients[0].source_state="revoked_verified",
    f=>f.clients[0].installation_refs=Array.from({length:17},(_,i)=>i.toString(16).padStart(32,"0")),f=>f.clients=Array.from({length:4097},()=>structuredClone(f.clients[0]))]){
    const f=registeredMigration();change(f);assert.throws(()=>validate.migration(f));
  }
});
