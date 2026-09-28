import { Route, Routes } from "react-router";
import {
  AccountPage,
  BlockPage,
  CommitteePage,
  EpochPage,
  Home,
  HousesPage,
  PoolsPage,
  SearchPage,
  SupplyPage,
  TxPage,
  ValidatorPage,
} from "../explorer/views";

export default function Explorer() {
  return (
    <Routes>
      <Route index element={<Home />} />
      <Route path="block/:height" element={<BlockPage />} />
      <Route path="tx/:hash" element={<TxPage />} />
      <Route path="epoch" element={<EpochPage />} />
      <Route path="account/:address" element={<AccountPage />} />
      <Route path="validator/:address" element={<ValidatorPage />} />
      <Route path="committee" element={<CommitteePage />} />
      <Route path="supply" element={<SupplyPage />} />
      <Route path="pools" element={<PoolsPage />} />
      <Route path="houses" element={<HousesPage />} />
      <Route path="search" element={<SearchPage />} />
    </Routes>
  );
}
