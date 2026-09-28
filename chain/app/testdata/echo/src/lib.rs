//! Tiny Orama-authored CosmWasm contract. It records instantiate and execute.
//! Built for the C9 wasmvm test. Not a third-party blob.
use cosmwasm_std::{entry_point, DepsMut, Empty, Env, MessageInfo, Response, StdResult};

#[entry_point]
pub fn instantiate(_deps: DepsMut, _env: Env, _info: MessageInfo, _msg: Empty) -> StdResult<Response> {
    Ok(Response::new().add_attribute("action", "instantiate"))
}

#[entry_point]
pub fn execute(_deps: DepsMut, _env: Env, _info: MessageInfo, _msg: Empty) -> StdResult<Response> {
    Ok(Response::new().add_attribute("action", "execute"))
}
