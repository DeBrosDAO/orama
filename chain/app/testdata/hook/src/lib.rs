//! Orama-authored token transfer hook, for the C10 hook test. Not a third-party blob.
//!
//! The chain calls `sudo` with `{"transfer_hook":{"denom","from","to","amount"}}` on every
//! `MsgTransfer` of a token that names this contract. A transfer of 13 is refused, a transfer of
//! 99 loops until the gas cap stops it, and any other amount is allowed.
use cosmwasm_std::{
    entry_point, Binary, Deps, DepsMut, Empty, Env, MessageInfo, Response, StdError, StdResult,
};
use serde::Deserialize;

const REFUSED_AMOUNT: &str = "13";
const LOOP_AMOUNT: &str = "99";

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SudoMsg {
    TransferHook { denom: String, from: String, to: String, amount: String },
}

#[entry_point]
pub fn instantiate(_deps: DepsMut, _env: Env, _info: MessageInfo, _msg: Empty) -> StdResult<Response> {
    Ok(Response::new())
}

#[entry_point]
pub fn query(_deps: Deps, _env: Env, _msg: Empty) -> StdResult<Binary> {
    Ok(Binary::default())
}

#[entry_point]
pub fn sudo(deps: DepsMut, _env: Env, msg: SudoMsg) -> StdResult<Response> {
    let SudoMsg::TransferHook { amount, .. } = msg;
    if amount == REFUSED_AMOUNT {
        return Err(StdError::generic_err("this hook refuses a transfer of 13"));
    }
    if amount == LOOP_AMOUNT {
        let mut n: u64 = 0;
        loop {
            deps.storage.set(b"n", &n.to_be_bytes());
            n = n.wrapping_add(1);
        }
    }
    Ok(Response::new().add_attribute("action", "transfer_hook"))
}
