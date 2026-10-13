//! Orama test relay. Executes and queries whatever it is told to, and stores what it is told to
//! store, so the C9 app tests can drive the bindings, the send restriction and the state-deposit
//! meter through a real wasmvm. Not a third-party blob and not a genesis contract.
use cosmwasm_std::{
    entry_point, to_json_vec, Binary, ContractResult, CosmosMsg, CustomMsg, DepsMut, Deps, Empty, Env,
    MessageInfo, QueryRequest, Reply, Response, StdError, StdResult, SubMsg, SystemResult,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// Any JSON object, carried through as the custom message of a CosmosMsg.
#[derive(Serialize, Deserialize, Clone, Debug, PartialEq)]
#[serde(transparent)]
pub struct Raw(pub Value);
impl CustomMsg for Raw {}
impl schemars::JsonSchema for Raw {
    fn schema_name() -> String {
        "Raw".to_string()
    }
    fn json_schema(_gen: &mut schemars::gen::SchemaGenerator) -> schemars::schema::Schema {
        schemars::schema::Schema::Bool(true)
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ExecuteMsg {
    /// Send every message, fire and forget.
    Dispatch { msgs: Vec<CosmosMsg<Raw>> },
    /// Send one message as a submessage and record its reply under `last_reply`.
    DispatchSub { id: u64, msg: CosmosMsg<Raw> },
    /// Write `value` under `key`.
    Store { key: String, value: String },
    /// Write `len` bytes under `key`.
    StoreBytes { key: String, len: u32 },
    /// Delete `key`.
    Remove { key: String },
    /// Spin until the gas runs out.
    Spin {},
}

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum QueryMsg {
    /// Answer with a query the chain runs on the contract's behalf.
    Forward { request: QueryRequest<Raw> },
    /// Read one stored key.
    Get { key: String },
}

#[entry_point]
pub fn instantiate(_deps: DepsMut, _env: Env, _info: MessageInfo, _msg: Empty) -> StdResult<Response> {
    Ok(Response::new().add_attribute("action", "instantiate"))
}

#[entry_point]
pub fn execute(deps: DepsMut, _env: Env, _info: MessageInfo, msg: ExecuteMsg) -> StdResult<Response<Raw>> {
    match msg {
        ExecuteMsg::Dispatch { msgs } => Ok(Response::new().add_messages(msgs)),
        ExecuteMsg::DispatchSub { id, msg } => Ok(Response::new().add_submessage(SubMsg::reply_always(msg, id))),
        ExecuteMsg::Store { key, value } => {
            deps.storage.set(key.as_bytes(), value.as_bytes());
            Ok(Response::new())
        }
        ExecuteMsg::StoreBytes { key, len } => {
            deps.storage.set(key.as_bytes(), &vec![7u8; len as usize]);
            Ok(Response::new())
        }
        ExecuteMsg::Remove { key } => {
            deps.storage.remove(key.as_bytes());
            Ok(Response::new())
        }
        ExecuteMsg::Spin {} => {
            let mut n: u64 = 0;
            loop {
                n = core::hint::black_box(n.wrapping_add(1));
            }
        }
    }
}

#[entry_point]
pub fn reply(deps: DepsMut, _env: Env, msg: Reply) -> StdResult<Response<Raw>> {
    let text = match msg.result {
        cosmwasm_std::SubMsgResult::Ok(_) => format!("ok:{}", msg.id),
        cosmwasm_std::SubMsgResult::Err(e) => format!("err:{}:{}", msg.id, e),
    };
    deps.storage.set(b"last_reply", text.as_bytes());
    Ok(Response::new())
}

#[entry_point]
pub fn query(deps: Deps, _env: Env, msg: QueryMsg) -> StdResult<Binary> {
    match msg {
        QueryMsg::Get { key } => Ok(deps.storage.get(key.as_bytes()).map(Binary::from).unwrap_or_default()),
        QueryMsg::Forward { request } => {
            let raw = to_json_vec(&request)?;
            match deps.querier.raw_query(&raw) {
                SystemResult::Err(e) => Err(StdError::generic_err(format!("system error: {}", e))),
                SystemResult::Ok(ContractResult::Err(e)) => Err(StdError::generic_err(format!("query error: {}", e))),
                SystemResult::Ok(ContractResult::Ok(bin)) => Ok(bin),
            }
        }
    }
}
