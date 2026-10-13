import { Route } from "react-router";
import { CodePage } from "./code";
import { CollectionPage } from "./collection";
import { ContractPage } from "./contract";
import { ContractsPage } from "./contracts";
import { GovernancePage } from "./governance";
import { ListingPage } from "./listing";
import { MarketPage } from "./market";
import { NftPage } from "./nft";
import { NftsPage } from "./nfts";
import { OwnedNftsPage } from "./owned-nfts";
import { ProposalPage } from "./proposal";
import { ShieldedPage } from "./shielded";
import { TokenPage } from "./token";
import { TokensPage } from "./tokens";

/** The routes of the token, NFT, contract, market, governance and shielded pages (see model/assets-routes.ts for their URLs). */
export const ASSET_ROUTES = (
  <>
    <Route path="tokens" element={<TokensPage />} />
    <Route path="token/:creator/:subdenom" element={<TokenPage />} />
    <Route path="nfts" element={<NftsPage />} />
    <Route path="nfts/:owner" element={<OwnedNftsPage />} />
    <Route path="nft/:id" element={<NftPage />} />
    <Route path="collection/:id" element={<CollectionPage />} />
    <Route path="contracts" element={<ContractsPage />} />
    <Route path="code/:id" element={<CodePage />} />
    <Route path="contract/:address" element={<ContractPage />} />
    <Route path="market" element={<MarketPage />} />
    <Route path="listing/:id" element={<ListingPage />} />
    <Route path="governance" element={<GovernancePage />} />
    <Route path="proposal/:id" element={<ProposalPage />} />
    <Route path="shielded" element={<ShieldedPage />} />
  </>
);
